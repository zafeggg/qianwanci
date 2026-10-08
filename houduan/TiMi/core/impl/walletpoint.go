package impl

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core"
	"com.fibonacci.crowd/model"
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"sync"
)

type WalletPointSafe struct {
	UpdateAmountLock sync.Mutex
}

func NewWalletPointSafe() core.WalletPointing {
	return &WalletPointSafe{}
}

func (w *WalletPointSafe) AddPointAmount(tx *gorm.DB, address string, symbol string, amount float64) (amountWithDecimal uint64, err error) {
	w.UpdateAmountLock.Lock()
	defer w.UpdateAmountLock.Unlock()

	if amount > 0 {
		var upSuccess = false
		var times int
		for !upSuccess && times < 100 {
			//换成精度值
			amountWithDecimal = uint64(amount * config.SymbolDictionary[symbol])
			var walletPoint = w.GetOrInitWalletPoint(tx, address, symbol)
			sqlUp := fmt.Sprintf("update %s set `amount` = `amount` + %d where `id` = %d and `amount` = %d", model.WalletPointTable, amountWithDecimal, walletPoint.ID, walletPoint.Amount)

			result := tx.Exec(sqlUp)
			if err = result.Error; err != nil {
				log.Errorln("concurrency add point amount failed err", address, symbol, amount, fmt.Sprintf("retry %d", times), err)
				times++
				continue
			}
			if ras := result.RowsAffected; ras <= 0 {
				log.Errorln("concurrency add point amount failed err", address, symbol, amount, fmt.Sprintf("retry %d", times), "affected rows <= 0")
				times++
				continue
			}

			upSuccess = true
		}

		if !upSuccess || times > 100 {
			err = errors.New("concurrency add point amount failed err")
			return
		}
	}
	return
}

func (w *WalletPointSafe) GetOrInitWalletPoint(tx *gorm.DB, address string, symbol string) model.WalletPoint {
	var walletPoint model.WalletPoint
	err := tx.Table(model.WalletPointTable).First(&walletPoint, "address = ? and symbol = ?", address, symbol).Error
	switch err {
	case gorm.ErrRecordNotFound:
		walletPoint.Address = address
		walletPoint.Symbol = symbol
		walletPoint.Amount = 0
		if err = tx.Table(model.WalletPointTable).Create(&walletPoint).Error; err != nil {
			return walletPoint
		}
	}
	return walletPoint
}

