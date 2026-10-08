package transaction

import (
	"bytes"
	"com.fibonacci.crowd/kortho/types"
	"com.fibonacci.crowd/kortho/util/miscellaneous"
	"crypto/ed25519"
	"errors"

	"golang.org/x/crypto/sha3"
)

type Transaction struct {
	//Nonce 自增的正整数，同一地址当前交易必定比上次大一
	Nonce uint64 `json:"nonce,omitempty"`

	// Amount 交易的金额
	Amount uint64 `json:"amount,omitempty"`

	// From 交易的发起方地址
	From types.Address `json:"from,omitempty"`

	// To 交易的接收方地址
	To types.Address `json:"to"`

	// Hash 交易hash
	Hash []byte `json:"hash,omitempty"`

	// Signature 交易的签名
	Signature []byte `json:"signature,omitempty"`

	// Time 发起交易的时间时间戳，以秒为单位
	Time int64 `json:"time,omitempty"`
}

// HashTransaction 对交易进行hash
func (tx *Transaction) HashTransaction() {
	fromBytes := tx.From[:]
	toBytes := tx.To[:]
	nonceBytes := miscellaneous.E64func(tx.Nonce)
	amountBytes := miscellaneous.E64func(tx.Amount)
	timeBytes := miscellaneous.E64func(uint64(tx.Time))
	txBytes := bytes.Join([][]byte{nonceBytes, amountBytes, fromBytes, toBytes, timeBytes}, []byte{})
	hash := sha3.Sum256(txBytes)
	tx.Hash = hash[:]
}

// Sign 用ed25519椭圆曲线签名算法，对交易进行签名
func (tx *Transaction) Sign(privateKey []byte) error {
	if len(privateKey) != ed25519.PrivateKeySize {
		return errors.New("invalid private key")
	}
	signatures := ed25519.Sign(ed25519.PrivateKey(privateKey), tx.Hash)
	tx.Signature = signatures
	return nil
}

// TrimmedCopy 有选择的对交易的字段进行拷贝，用以验证签名
func (tx *Transaction) TrimmedCopy() *Transaction {
	txCopy := &Transaction{
		Nonce:  tx.Nonce,
		Amount: tx.Amount,
		From:   tx.From,
		To:     tx.To,
		Time:   tx.Time,
	}
	return txCopy
}

// Verify 验证签名，成功返回true，否则返回false
func (tx *Transaction) Verify() bool {
	txCopy := tx.TrimmedCopy()
	txCopy.HashTransaction()
	publicKey := tx.From.ToPublicKey()
	if len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(publicKey, txCopy.Hash, tx.Signature)
}
