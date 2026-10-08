package impl

import (
	"bytes"
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/gofiber/fiber/v2"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"strconv"
	"testing"
	"time"
)

func Read() {
	path := flag.String("f", "/Users/zhengjianfeng_1/go/src/crowd/config/etc.yml", "-f 指定配置文件")
	flag.Parse()
	config.Init(*path) //mysql, redis, config file
}


func TestCreateTronAccount(t *testing.T) {
	url := "http://localhost:1527/createAccount"
	response, err := httpClient.Post(url, "application/json", bytes.NewReader([]byte("")))
	if err != nil {
		t.Fatal("post err", err)
	}
	if response.StatusCode != 200 {
		t.Fatal("status err", err)
	}

	bs, err := ioutil.ReadAll(response.Body)
	if err != nil {
		t.Fatal("read body err", err)
	}

	var account fiber.Map
	err = json.Unmarshal(bs, &account)
	if err != nil {
		t.Fatal("unmarshal body err", err)
	}

	address := account["address"].(map[string]interface{})
	pubKey := address["base58"]
	privateKey := account["privateKey"]

	log.Info("pubKey: ", pubKey, ", privateKey: ", privateKey)
}

func TestTronTransfer(t *testing.T) {
	url := "http://localhost:1527/transfer"
	// 2026-10-08 安全修复：from 原硬编码私钥字面量已移除，改环境变量注入（存档仅保留用例骨架）。
	from := os.Getenv("TRON_POOL_PRIVATE_KEY")
	if from == "" {
		t.Skip("未设置 TRON_POOL_PRIVATE_KEY，跳过")
	}
	request := make(map[string]interface{})
	request["from"] = from
	request["to"] = "TZByEVsU8W19avemsvetLt7GBy9pbaQZrY"
	request["amount"] = 1 * config.SymbolDictionary[enum.USDT]

	var rbs []byte
	var err error
	if rbs, err = json.Marshal(request); err != nil {
		t.Fatal("marshal request err",err)
	}
	response, err := httpClient.Post(url, "application/json", bytes.NewReader(rbs))
	if err != nil {
		t.Fatal("post err", err)
	}
	if response.StatusCode != 200 {
		t.Fatal("status err", err)
	}

	bs, err := ioutil.ReadAll(response.Body)
	if err != nil {
		t.Fatal("read body err", err)
	}

	var account fiber.Map
	err = json.Unmarshal(bs, &account)
	if err != nil {
		t.Fatal("unmarshal body err", err)
	}

	address := account["address"].(map[string]interface{})
	pubKey := address["base58"]
	privateKey := account["privateKey"]

	log.Info("pubKey: ", pubKey, ", privateKey: ", privateKey)
}


func TestWalletManager_Promote_level1_error(t *testing.T) {
	//invited 10 people
	//join a project round
	//invoke promote level1

	Read()

	//start a project
	projectManager := NewProjectManager()
	project, err := projectManager.Init("FIBO", 5)
	if err != nil {
		t.Fatal("init project failed", err)
	}
	err = projectManager.Start(project)
	if err != nil {
		t.Fatal("start project failed", err)
	}


	roundManager := NewRoundManager()
	projectId := project.ID
	target := 100.0
	min := 1.0
	max := 5.0
	startTime := time.Now().Add(1 * time.Second)
	endTime := time.Now().Add(2 * 60 * time.Second)
	projectRound, err := roundManager.Init(projectId, target, min, max, startTime, endTime)
	if err != nil {
		t.Fatal("init project round failed", err)
	}

	//waiting start project
	time.Sleep(1 * time.Second)
	err = roundManager.Begin(projectRound)
	if err != nil {
		t.Fatal("begin project round failed", err)
	}

	walletManager := NewWalletManager()
	mainWallet, err := walletManager.Registration("Main_wallet", "99999999", "123456")
	if err != nil {
		t.Fatal("register wallet failed", err)
	}
	//charge 1 fibo
	err = walletManager.Charge(mainWallet, "ktoxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "FIBO", 1, "0xdjflaisjdflkas")
	if err != nil {
		t.Fatal("charge wallet failed", err)
	}

	t.Log("main wallet level promote before: ", mainWallet.Level)

	//invited 100 wallet for main wallet and vote
	voteManager := NewVoteManager()
	_, err = voteManager.Vote(mainWallet, projectRound, 1)
	if err != nil {
		t.Fatal("vote main wallet failed", err)
	}

	//尝试提升钱包level
	err = walletManager.Promote(mainWallet.ID, projectRound.ProjectId)
	if err != nil {
		t.Fatal("promote main wallet err", err)
	}
	t.Log("main wallet level promote after: ", mainWallet.Level)
}

func TestWalletManager_Promote_level1(t *testing.T) {
	//invited 10 people
	//join a project round
	//invoke promote level1

	Read()

	//start a project
	projectManager := NewProjectManager()
	project, err := projectManager.Init("FIBO", 7)
	if err != nil {
		t.Fatal("init project failed", err)
	}
	err = projectManager.Start(project)
	if err != nil {
		t.Fatal("start project failed", err)
	}

	//start round
	roundManager := NewRoundManager()
	projectId := project.ID
	target := 100.0
	min := 1.0
	max := 5.0
	startTime := time.Now().Add(1 * time.Second)
	endTime := time.Now().Add(2 * 60 * time.Second)
	projectRound, err := roundManager.Init(projectId, target, min, max, startTime, endTime)
	if err != nil {
		t.Fatal("init project round failed", err)
	}

	//waiting start project
	time.Sleep(1 * time.Second)
	err = roundManager.Begin(projectRound)
	if err != nil {
		t.Fatal("begin project round failed", err)
	}

	walletManager := NewWalletManager()
	mainWallet, err := walletManager.Registration("Main_wallet", "99999999", "123456")
	if err != nil {
		t.Fatal("register afterWallet failed", err)
	}
	//charge 1 fibo
	err = walletManager.Charge(mainWallet, "ktoxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "FIBO", 1, "0xdjflaisjdflkas")
	if err != nil {
		t.Fatal("charge afterWallet failed", err)
	}

	t.Log("main afterWallet level promote before: ", mainWallet.Level)

	//invited 100 afterWallet for main afterWallet and vote
	voteManager := NewVoteManager()
	_, err = voteManager.Vote(mainWallet, projectRound, 1)
	if err != nil {
		t.Fatal("vote main afterWallet failed", err)
	}

	for i := 0; i < 11; i++ {
		inviteeWallet, err := walletManager.Registration("Main_wallet_invitee_"+ strconv.Itoa(i), mainWallet.Code, "123456")
		if err != nil {
			t.Fatal("invitee afterWallet failed", err)
		}
		err = walletManager.Charge(inviteeWallet, "ktoxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "FIBO", 1, "0xdjflaisjdflkas")
		if err != nil {
			t.Fatal("charge afterWallet failed", err)
		}

		//vote....
		_, err = voteManager.Vote(inviteeWallet, projectRound, 1)
		if err != nil {
			t.Fatal("vote main afterWallet failed", err)
		}

		for i := 0; i < 11; i++ {
			inviteeInviteeWallet, err := walletManager.Registration("Main_wallet_invitee_invitee"+ strconv.Itoa(i), inviteeWallet.Code, "123456")
			if err != nil {
				t.Fatal("invitee invitee afterWallet failed", err)
			}
			err = walletManager.Charge(inviteeInviteeWallet, "ktoxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "FIBO", 1, "0xdjflaisjdflkas")
			if err != nil {
				t.Fatal("charge afterWallet failed", err)
			}

			//vote....
			_, err = voteManager.Vote(inviteeInviteeWallet, projectRound, 1)
			if err != nil {
				t.Fatal("vote main afterWallet failed", err)
			}
		}
	}

	//尝试提升钱包level
	err = walletManager.Promote(mainWallet.ID, projectRound.ProjectId)
	if err != nil {
		t.Fatal("promote main afterWallet err", err)
	}

	var afterWallet model.Wallet
	if err = config.MysqlDBPool.Table(model.WalletTable).First(&afterWallet, "`id` = ?", mainWallet.ID).Error; err != nil {
		t.Fatal("afterWallet not found err", err)
	}
	t.Log("main afterWallet level promote after: ", afterWallet.Level)
	if afterWallet.Level != enum.Level1 {
		t.Fatal("promote level1 failed")
	}
}

func TestWalletManager_Promote_level2(t *testing.T) {
	//invited 10 people
	//join a project round
	//invoke promote level1

	Read()

	//start a project
	projectManager := NewProjectManager()
	project, err := projectManager.Init("FIBO", 9)
	if err != nil {
		t.Fatal("init project failed", err)
	}
	err = projectManager.Start(project)
	if err != nil {
		t.Fatal("start project failed", err)
	}

	//start round
	roundManager := NewRoundManager()

	projectId := project.ID
	target := 100.0
	min := 1.0
	max := 5.0
	startTime := time.Now().Add(1 * time.Second)
	endTime := time.Now().Add(2 * 60 * time.Second)
	projectRound, err := roundManager.Init(projectId, target, min, max, startTime, endTime)
	if err != nil {
		t.Fatal("init project round failed", err)
	}

	//waiting start project
	time.Sleep(1 * time.Second)
	err = roundManager.Begin(projectRound)
	if err != nil {
		t.Fatal("begin project round failed", err)
	}

	walletManager := NewWalletManager()
	mainWallet, err := walletManager.Registration("Main_wallet", "99999999", "123456")
	if err != nil {
		t.Fatal("register afterWallet failed", err)
	}
	//charge 1 fibo
	err = walletManager.Charge(mainWallet, "ktoxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "FIBO", 1, "0xdjflaisjdflkas")
	if err != nil {
		t.Fatal("charge afterWallet failed", err)
	}

	t.Log("main afterWallet level promote before: ", mainWallet.Level)

	//invited 100 afterWallet for main afterWallet and vote
	voteManager := NewVoteManager()
	_, err = voteManager.Vote(mainWallet, projectRound, 1)
	if err != nil {
		t.Fatal("vote main afterWallet failed", err)
	}

	var promoteWallet model.Wallet
	for i := 0; i < 11; i++ {

		inviteeWallet, err := walletManager.Registration("Main_wallet_invitee_"+ strconv.Itoa(i), mainWallet.Code, "123456")
		if err != nil {
			t.Fatal("invitee afterWallet failed", err)
		}
		err = walletManager.Charge(inviteeWallet, "ktoxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "FIBO", 1, "0xdjflaisjdflkas")
		if err != nil {
			t.Fatal("charge afterWallet failed", err)
		}

		//vote....
		_, err = voteManager.Vote(inviteeWallet, projectRound, 1)
		if err != nil {
			t.Fatal("vote main afterWallet failed", err)
		}

		if i < 3 {
			err = walletManager.SetLevel(inviteeWallet, enum.Level2)
			if err != nil {
				t.Fatal("set level failed", err)
			}
		}

		for i := 0; i < 11; i++ {
			inviteeInviteeWallet, err := walletManager.Registration("Main_wallet_invitee_invitee"+ strconv.Itoa(i), inviteeWallet.Code, "123456")
			if err != nil {
				t.Fatal("invitee invitee afterWallet failed", err)
			}
			err = walletManager.Charge(inviteeInviteeWallet, "ktoxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "FIBO", 1, "0xdjflaisjdflkas")
			if err != nil {
				t.Fatal("charge afterWallet failed", err)
			}

			//vote....
			_, err = voteManager.Vote(inviteeInviteeWallet, projectRound, 1)
			if err != nil {
				t.Fatal("vote main afterWallet failed", err)
			}
		}



			if i == 2 {
				promoteWallet = inviteeWallet

				for i := 0; i < 11; i++ {

					inviteeWallet, err := walletManager.Registration("Main_wallet_invitee_set_level1_"+ strconv.Itoa(i), promoteWallet.Code, "123456")
					if err != nil {
						t.Fatal("invitee afterWallet failed", err)
					}
					err = walletManager.Charge(inviteeWallet, "ktoxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "FIBO", 1, "0xdjflaisjdflkas")
					if err != nil {
						t.Fatal("charge afterWallet failed", err)
					}

					//vote....
					_, err = voteManager.Vote(inviteeWallet, projectRound, 1)
					if err != nil {
						t.Fatal("vote main afterWallet failed", err)
					}

					if i < 3 {
						err = walletManager.SetLevel(inviteeWallet, enum.Level1)
						if err != nil {
							t.Fatal("set level failed", err)
						}
					}


					for i := 0; i < 11; i++ {
						inviteeInviteeWallet, err := walletManager.Registration("Main_wallet_invitee_set_level1_invitee"+ strconv.Itoa(i), inviteeWallet.Code, "123456")
						if err != nil {
							t.Fatal("invitee invitee afterWallet failed", err)
						}
						err = walletManager.Charge(inviteeInviteeWallet, "ktoxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "FIBO", 1, "0xdjflaisjdflkas")
						if err != nil {
							t.Fatal("charge afterWallet failed", err)
						}

						//vote....
						_, err = voteManager.Vote(inviteeInviteeWallet, projectRound, 1)
						if err != nil {
							t.Fatal("vote main afterWallet failed", err)
						}
					}
				}
			}
	}

	//尝试提升钱包level
	t.Log("promote wallet info: ", promoteWallet)
	err = walletManager.Promote(mainWallet.ID, projectRound.ProjectId)
	if err != nil {
		t.Fatal("promote main afterWallet err", err)
	}

	var afterWallet model.Wallet
	if err = config.MysqlDBPool.Table(model.WalletTable).First(&afterWallet, "`id` = ?", mainWallet.ID).Error; err != nil {
		t.Fatal("afterWallet not found err", err)
	}
	t.Log("main afterWallet level promote after: ", afterWallet.Level)
	if afterWallet.Level != enum.Level2 {
		t.Fatal("promote level1 failed")
	}
}

func TestWalletManager_Invite(t *testing.T) {
	Read()

	walletManager := NewWalletManager()
	_, err := walletManager.Registration("钱包二", "K547X6DH", "123456")
	if err != nil {
		t.Fatal("registration wallet failed.", err)
	}

}

func TestWalletManager_Recover(t *testing.T) {
	Read()

	walletManager := NewWalletManager()
	signWallet, err := walletManager.Registration("Recover", "99999999", "123456")
	if err != nil {
		t.Fatal("register wallet failed", err)
	}

	sign := signWallet.Sign
	t.Log("sign", sign)
	recoverWallet, err := walletManager.Recover(sign, "123456")
	if err != nil {
		t.Fatal("recover wallet failed", err)
	}

	if signWallet.Address != recoverWallet.Address {
		t.Fatal("recover wallet failed", "address not equal")
	}
}

func TestWalletManager_Recover_password_error(t *testing.T) {
	Read()

	walletManager := NewWalletManager()
	signWallet, err := walletManager.Registration("Recover", "99999999", "123456")
	if err != nil {
		t.Fatal("register wallet failed", err)
	}

	sign := signWallet.Sign
	recoverWallet, err := walletManager.Recover(sign, "12345")
	if err != nil {
		t.Fatal("recover wallet failed", err)
	}

	if signWallet.Address != recoverWallet.Address {
		t.Fatal("recover wallet failed", "address not equal")
	}
}

func TestWalletManager_Charge(t *testing.T) {
	Read()

	fromAddress := "Kto7A1FCdp2c3endgUsTLNQzGtHjGNmytueQaJCsX5FKjmi"
	chargeAddress := "Kto4LKgNyGbPpEN5zFA85kyCYhr5QHTXVa1gjhZDnxkvk2h"
	var wallet model.Wallet
	var err error
	if err  = config.MysqlDBPool.Table(model.WalletTable).First(&wallet, "`address` = ?", chargeAddress).Error; err != nil {
		t.Fatal("wallet not found", err)
	}

	walletManager := NewWalletManager()

	err = walletManager.Charge(wallet, fromAddress, "FIBO", 1000_00000000, "0xsdjggghfaldsssssjfliassd")
	if err != nil {
		t.Fatal("充值失败", err)
	}
}

func TestIf(t *testing.T) {
	for i := 0; i < 10; i++ {
		fmt.Println("i", i)
		if i > 0 {
			if i > 5 {
				return
			}
		}

	}
}