package types

import (
	"encoding/hex"
	"testing"
)

func TestNewWallet(t *testing.T) {
	w := NewWallet()
	if len(w.Address) != AddressSize {
		t.Error("size error")
	}
	t.Log(w.Address)
	t.Log(hex.EncodeToString(w.PrivateKey))
}
