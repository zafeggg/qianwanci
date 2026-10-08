package transaction

import (
	"com.fibonacci.crowd/kortho/types"
	"testing"
	"time"
)

func TestHash(t *testing.T) {
	w := types.NewWallet()
	from, _ := types.StringToAddress(w.Address)
	to, _ := types.StringToAddress(types.NewWallet().Address)
	tx := &Transaction{
		Amount: 100,
		From:   *from,
		To:     *to,
		Time:   time.Now().Unix(),
		Nonce:  1,
	}
	tx.HashTransaction()
	tx.Sign(w.PrivateKey)
	if !tx.Verify() {
		t.Error("sign error")
	}
}
