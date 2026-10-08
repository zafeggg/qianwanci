package impl

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"
	"com.fibonacci.crowd/utils/kto"
	"com.fibonacci.crowd/utils/tron"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/bsm/redislock"
	"github.com/go-redis/redis/v8"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"math/rand"
	"time"
)

type WalletManager struct {
	DB              *gorm.DB
	WalletModel     *model.WalletModel
	RedisClient     *redis.Client
	Locker          *redislock.Client
	WalletPointSafe core.WalletPointing
}

func (w *WalletManager) ChargePoint(address string, symbol string, amount uint64) error {
	var walletPoint model.WalletPoint
	err := w.DB.Table(model.WalletPointTable).First(&walletPoint, "address = ? and symbol = ?", address, symbol).Error
	switch err {
	case gorm.ErrRecordNotFound:
		walletPoint.Address = address
		walletPoint.Symbol = symbol
		walletPoint.Amount = amount
		if err = w.DB.Table(model.WalletPointTable).Create(&walletPoint).Error; err != nil {
			return err
		}
	case nil:
		//修复存量bug：记录已存在时应走 Updates 而非 Create，避免重复主键错误
		if err = w.DB.Table(model.WalletPointTable).Where("id = ?", walletPoint.ID).
			Update("amount", walletPoint.Amount+amount).Error; err != nil {
			return err
		}
	}
	return err
}

func (w *WalletManager) SetLevel(wallet model.Wallet, level int) error {
	switch level {
	case enum.Level0:
		return errors.New("level 0 error")
	case enum.Level1:
	case enum.Level2:
	case enum.Level3:
	default:
		return errors.New("level error")
	}
	if err := w.DB.Table(model.WalletTable).Where("`id` = ?", wallet.ID).Update("level", level).Error; err != nil {
		return err
	}
	log.WithFields(log.Fields{"Method": "SetLevel", "walletId": wallet.ID}).Infoln("升级Level成功")
	return nil
}

func (w *WalletManager) Registration(name string, code string, password string) (wallet model.Wallet, err error) {
	var ktoPub string
	var ktoPri string
	var tronPub string
	var tronPri string
	//1.生成波场钱包
	if tronPub, tronPri, err = tron.CreateAccount(); err != nil {
		err = errors.New("创建钱包失败")
		return
	}

	//2.生成kto钱包
	if ktoPub, ktoPri, err = kto.CreateWallet(); err != nil {
		err = errors.New("创建钱包失败")
		return
	}

	//3.用kto作为主钱包
	wallet.Address = ktoPub
	wallet.Private = ktoPri
	wallet.TronAddress = tronPub
	wallet.TronPrivate = tronPri
	wallet.Name = name

	var encryptPwd string
	if encryptPwd = utils.HashAndSalt([]byte(password)); err != nil {
		return
	}
	//4.加密私钥 需要密码解密
	var sign []byte
	if sign, err = utils.AesEncrypt([]byte(ktoPri), []byte(utils.SECRET_KEY)); err != nil {
		return
	}
	wallet.Password = encryptPwd
	wallet.Sign = hex.EncodeToString(sign)

	if code == "" {
		err = errors.New("邀请码不能为空")
		return
	}

	var fromWallet model.Wallet
	err = w.DB.Table(model.WalletTable).First(&fromWallet, "code = ?", code).Error
	switch err {
	case nil:
	case gorm.ErrRecordNotFound:
		err = errors.New("邀请人邀请码不存在")
		return
	}

	rand.Seed(time.Now().UnixNano())
	fromCode := utils.GetString(8)
	err = w.DB.Transaction(func(tx *gorm.DB) error {
		wallet.Code = fromCode
		if err = tx.Model(model.Wallet{}).Create(&wallet).Error; err != nil {
			return err
		}

		return w.InviteWithTx(tx, fromWallet.ID, wallet.ID)
	})

	return
}

func (w *WalletManager) Recover(sign string, password string) (wallet model.Wallet, err error) {

	//1.use password decrypt sign
	var decodeHex []byte
	var decrypt []byte
	if decodeHex, err = hex.DecodeString(sign); err != nil {
		return
	}
	if decrypt, err = utils.AesDecrypt(decodeHex, []byte(utils.SECRET_KEY)); err != nil {
		err = errors.New("私钥错误")
		return
	}

	if err = w.DB.Table(model.WalletTable).First(&wallet, "private = ?", string(decrypt)).Error; err != nil {
		err = errors.New("错误的私钥")
		return
	}
	if !utils.ComparePasswords(wallet.Password, []byte(password)) {
		err = errors.New("密码错误")
		return
	}
	return
}

func (w *WalletManager) InviteWithTx(tx *gorm.DB, fromWalletId uint, toWalletId uint) error {
	ancestorId := fromWalletId
	descendantId := toWalletId
	var err error
	//复制父节点的所有记录，distance+1
	//把descendantId 插入到ancestorId节点下
	sql := fmt.Sprintf("INSERT INTO %v (`ancestor`, `descendant`, `distance`) (SELECT `ancestor`, ?, `distance` + 1 FROM %v WHERE `descendant` = ?)", model.WalletTreeTable, model.WalletTreeTable)
	if err = tx.Exec(sql, descendantId, ancestorId).Error; err != nil {
		return err
	}

	//父→子直连边 (P,D,0)。本库闭包表采用偏移约定：distance=0 即直推（DirectDescendants / FindAncestorPathAge 均按此查询），
	//树中不存自环。InviteWithTx（注册路径）原实现即正确，保持不动
	sql2 := fmt.Sprintf("INSERT INTO %v (`ancestor`, `descendant`, `distance`) VALUES(?, ?, ?)", model.WalletTreeTable)
	if err = tx.Exec(sql2, ancestorId, descendantId, 0).Error; err != nil {
		return err
	}
	return nil
}

func (w *WalletManager) Invite(fromCode string, toCode string) error {
	//建立邀请关系
	var fromWallet model.Wallet
	var toWallet model.Wallet
	var err error
	err = w.DB.Table(model.WalletTable).First(&fromWallet, "code = ?", fromCode).Error
	switch err {
	case nil:
	case gorm.ErrRecordNotFound:
		return errors.New("邀请人邀请码不存在")
	}
	err = w.DB.Table(model.WalletTable).First(&toWallet, "code = ?", toCode).Error
	switch err {
	case nil:
	case gorm.ErrRecordNotFound:
		return errors.New("被邀请人邀请码不存在")
	}

	ancestorId := fromWallet.ID
	descendantId := toWallet.ID

	if err = w.DB.Transaction(func(tx *gorm.DB) error {

		//复制父节点的所有记录，distance+1
		//把descendantId 插入到ancestorId节点下
		sql := fmt.Sprintf("INSERT INTO %v (`ancestor`, `descendant`, `distance`) (SELECT `ancestor`, ?, `distance` + 1 FROM %v WHERE `descendant` = ?)", model.WalletTreeTable, model.WalletTreeTable)
		if err = tx.Exec(sql, descendantId, ancestorId).Error; err != nil {
			return err
		}

		//加入自身节点
		sql2 := fmt.Sprintf("INSERT INTO %v (`ancestor`, `descendant`, `distance`) VALUES(?, ?, ?)", model.WalletTreeTable)
		if err = tx.Exec(sql2, descendantId, descendantId, 0).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (w *WalletManager) Charge(wallet model.Wallet, from string, symbol string, amount uint64, hash string) error {
	keyPre := "charge_" + wallet.Address
	lock, err := w.Locker.Obtain(context.Background(), keyPre, 100*time.Millisecond, nil)
	if err == redislock.ErrNotObtained {
		return errors.New("充值执行中 请稍后")
	} else if err != nil {
		return err
	}
	// Don't forget to defer Release.
	defer lock.Release(context.Background())

	//1.为钱包充值
	var walletPoint model.WalletPoint
	err = w.DB.Table(model.WalletPointTable).First(&walletPoint, "address = ? and symbol = ?", wallet.Address, symbol).Error
	switch err {
	case gorm.ErrRecordNotFound:
		walletPoint.Address = wallet.Address
		walletPoint.Symbol = symbol
		if err = w.DB.Table(model.WalletPointTable).Create(&walletPoint).Error; err != nil {
			return err
		}
	case nil:
	}

	//3.更新状态
	if err = w.DB.Transaction(func(tx *gorm.DB) error {

		var wch model.WalletHashCharge
		err = tx.Table(model.WalletHashChargeTable).First(&wch, "`address` = ? and `hash` = ? and `symbol` = ?", wallet.Address, hash, symbol).Error
		switch err {
		case gorm.ErrRecordNotFound:
			address := wallet.Address
			//2.充入余额
			if amount > 0 {
				amountF := float64(amount) / config.SymbolDictionary[symbol]
				if _, err = w.WalletPointSafe.AddPointAmount(tx, address, symbol, amountF); err != nil {
					return err
				}
			}

			wtx := new(model.WalletTx)
			wtx.Hash = hash
			wtx.Symbol = symbol
			wtx.From = from
			if symbol == enum.USDT {
				wtx.To = wallet.TronAddress
			}else{
				wtx.To = wallet.Address
			}
			wtx.Amount = amount
			wtx.Desc = enum.BlockInText
			wtx.Type = enum.BlockIn
			if err = tx.Table(model.WalletTxTable).Create(wtx).Error; err != nil {
				return err
			}

			wch.Address = wallet.Address
			wch.Hash = hash
			wch.Symbol = symbol
			wch.Amount = amount
			if err = tx.Table(model.WalletHashChargeTable).Create(&wch).Error; err != nil {
				return err
			}

			//3.归集 fund collection
			data := enum.FundCollectionData{
				Address:         from,
				KtoAddr:         wallet.Address,
				KtoAddrPrivate:  wallet.Private,
				TronAddr:        wallet.TronAddress,
				TronAddrPrivate: wallet.TronPrivate,
				IsTron:          symbol == enum.USDT,
				Symbol:          symbol,
				Amount:          amount,
			}

			_, err = w.RedisClient.Publish(context.Background(), enum.FundCollectionQueue, data).Result()
			if err != nil {
				log.Errorln("l push fund collection queue err", err)
			}
		case nil:
			log.Errorln("充值Hash: ", hash, "已存在")
		}

		return nil
	}); err != nil {
		return err
	}

	return nil
}

func (w *WalletManager) Promote(voteWalletId uint, projectId uint) error {
	//1. f1 标准: 直推10人, 并且伞下100人参与该项目投资
	//2. f2 标准: 伞下3个f1
	//3. f3 标准: 伞下3个f2
	var err error
	mayToF1FatherPath, err := w.WalletModel.FindAncestorPath(voteWalletId)
	if err != nil {
		return err
	}
	for _, mayToF1Father := range mayToF1FatherPath {
		var wallet model.Wallet
		if err = w.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", mayToF1Father).Error; err != nil {
			log.WithFields(log.Fields{"mayToF1Father": mayToF1Father}).Infoln("try wallet info err", err)
			return err
		}

		switch wallet.Level {
		case enum.Level0:
			log.WithFields(log.Fields{"mayToF1Father": mayToF1Father}).Infoln("try to promote level1")
			var directInviteeCount uint
			if err = w.DB.Table(model.WalletTreeTable).Select("count(`descendant`)").Where("`ancestor` = ? and `distance` = 0", mayToF1Father).Scan(&directInviteeCount).Error; err != nil {
				return err
			}
			isDirectInvitee10 := directInviteeCount >= F1DirectCount

			//伞下用户
			var inviteeIdList []uint
			if err = w.DB.Table(model.WalletTreeTable).Select("`descendant`").Where("`ancestor` = ? and `distance` >= 0", mayToF1Father).Find(&inviteeIdList).Error; err != nil {
				return err
			}

			//伞下人数判断
			if len(inviteeIdList) < F1VoteCount {
				log.WithFields(log.Fields{"mayToF1Father": mayToF1Father, "方法": "获取伞下所有用户信息"}).Infoln(fmt.Sprintf("伞下人数不超过%v", F1VoteCount))
				continue
			}

			//该期项目下 伞下参与人数
			voteCount := 0
			if err = w.DB.Table(model.VoteTable).Select("count(distinct `wallet_id`)").Find(&voteCount, "`wallet_id` IN ? and `project_id` = ?", inviteeIdList, projectId).Error; err != nil {
				log.WithFields(log.Fields{"mayToF1Father": mayToF1Father, "方法": "该期项目下 伞下参与人数"}).Errorln(err)
				return err
			}

			//N次方改造项7：伞下历史累计成功人数。
			//口径(经确认)：历史累计。伞下用户曾在任意期、任意轮参与过 success=1 的成功轮结算，即计为"成功至少1次"。
			//实现：vote 表按 wallet_id join project_round 表的 success 标志，COUNT(DISTINCT wallet_id)。
			//不选 vote_reward.Static>0 判定的原因：语义等价，但 join 轮状态不依赖结算行生成时序，更直接可靠。
			//说明：F1 晋升恰在当期成功轮结算中触发，若限定"该项目期内成功"则刚结算的参与者自动满足，校验空转，故用历史累计口径。
			var successCount int64
			if err = w.DB.Table(model.VoteTable+" v").
				Joins("JOIN "+model.ProjectRoundTable+" pr ON pr.id = v.round_id").
				Where("v.wallet_id IN ? AND pr.success = ?", inviteeIdList, true).
				Distinct("v.wallet_id").
				Count(&successCount).Error; err != nil {
				log.WithFields(log.Fields{"mayToF1Father": mayToF1Father, "方法": "伞下历史累计成功人数"}).Errorln(err)
				return err
			}

			if isDirectInvitee10 && voteCount >= F1VoteCount && successCount >= F1VoteCount {
				if err = w.DB.Table(model.WalletTable).Where("`id` = ?", mayToF1Father).Update("level", enum.Level1).Error; err != nil {
					log.Errorln("update level1 err", mayToF1Father, err)
					continue
				}
				log.WithFields(log.Fields{"方法": "推动提升Level1等级", "walletId": mayToF1Father}).Infoln("成功推升成为F1")

				//推升F1同时 找到所有父节点 尝试推升父节点为F2
				var mayToF2FatherPath []uint
				if mayToF2FatherPath, err = w.WalletModel.FindAncestorPath(mayToF1Father); err != nil {
					log.WithFields(log.Fields{"方法": "推动提升Level2等级"}).Errorln("find path", err)
					return err
				}
				log.WithFields(log.Fields{"方法": "准备推动提升Level2等级", "mayToF1Father": mayToF1Father}).Infoln("find mayToF2FatherPath path", mayToF2FatherPath)
				for _, mayToF2Father := range mayToF2FatherPath {
					//找每个父节点伞下用户, 尝试推升等级F2
					//伞下直推路径
					var ancestorDirectInvitees []int64
					if err = w.DB.Table(model.WalletTreeTable).Select("descendant").Where("`ancestor` = ? and `distance` = 0", mayToF2Father).Find(&ancestorDirectInvitees).Error; err != nil {
						return err
					}

					//直推路径的f1个数统计
					var f1Count uint
					for _, ancestorDirect := range ancestorDirectInvitees {
						var ancestorDirectInvitees []int64
						if err = w.DB.Table(model.WalletTreeTable).Select("descendant").Where("`ancestor` = ? and `distance` >= 0", ancestorDirect).Find(&ancestorDirectInvitees).Error; err != nil {
							return err
						}
						ancestorDirectInvitees = append(ancestorDirectInvitees, ancestorDirect)

						var ancestorInviteeTrees []model.Wallet
						if err = w.DB.Table(model.WalletTable).Find(&ancestorInviteeTrees, "`id` IN ?", ancestorDirectInvitees).Error; err != nil {
							return err
						}

						for _, at := range ancestorInviteeTrees {
							if at.Level >= enum.Level1 {
								f1Count++
								break
							}
						}

						if f1Count >= F1ToF2Count {
							break
						}
					}
					//满足提升f2个数
					if f1Count >= F1ToF2Count {
						if err = w.DB.Table(model.WalletTable).Where("`id` = ?", mayToF2Father).Update("level", enum.Level2).Error; err != nil {
							log.Errorln("update level2 err", mayToF1Father, err)
							continue
						}
						log.WithFields(log.Fields{"方法": "推动提升Level2等级", "mayToF2Father": mayToF2Father}).Infoln("成功推升成为L2")

						//推升F2同时 找到所有父节点 尝试推升父节点为F3
						var mayToF3FatherPath []uint
						if mayToF3FatherPath, err = w.WalletModel.FindAncestorPath(mayToF2Father); err != nil {
							log.WithFields(log.Fields{"方法": "推动提升Level2等级"}).Errorln("find path", err)
							return err
						}

						for _, mayToF3Father := range mayToF3FatherPath {
							//找每个父节点伞下用户, 尝试推升等级F3 , 伞下直推路径
							var ancestorDirectInvitees []int64
							if err = w.DB.Table(model.WalletTreeTable).Select("descendant").Where("`ancestor` = ? and `distance` = 0", mayToF3Father).Find(&ancestorDirectInvitees).Error; err != nil {
								return err
							}

							//直推路径的f1个数统计
							var f2Count uint
							for _, ancestorDirect := range ancestorDirectInvitees {
								var ancestorDirectInvitees []int64
								if err = w.DB.Table(model.WalletTreeTable).Select("descendant").Where("`ancestor` = ? and `distance` >= 0", ancestorDirect).Find(&ancestorDirectInvitees).Error; err != nil {
									return err
								}
								ancestorDirectInvitees = append(ancestorDirectInvitees, ancestorDirect)

								var ancestorInviteeTrees []model.Wallet
								if err = w.DB.Table(model.WalletTable).Find(&ancestorInviteeTrees, "`id` IN ?", ancestorDirectInvitees).Error; err != nil {
									return err
								}
								for _, at := range ancestorInviteeTrees {
									if at.Level >= enum.Level2 {
										f2Count++
										break
									}
								}
								if f2Count >= F2ToF3Count {
									break
								}
							}

							if f2Count >= F2ToF3Count {
								if err = w.DB.Table(model.WalletTable).Where("`id` = ?", mayToF3Father).Update("level", enum.Level3).Error; err != nil {
									log.Errorln("update level3 err", mayToF1Father, err)
									continue
								}
								log.WithFields(log.Fields{"方法": "推动提升Level3等级", "mayToF3Father": mayToF3Father}).Infoln("成功推升成为L3")
							}
						}

					}
				}

			}
		}
	}

	return nil
}

func NewWalletManager() core.OnBoarding {
	return &WalletManager{
		RedisClient:     config.RedisClient,
		Locker:          redislock.New(config.RedisClient),
		DB:              config.MysqlDBPool,
		WalletModel:     model.NewWalletModel(),
		WalletPointSafe: NewWalletPointSafe(),
	}
}
