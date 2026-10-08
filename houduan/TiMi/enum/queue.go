package enum

import "encoding/json"

const FundCollectionQueue = "FundCollectionQueue"

type FundCollectionData struct {
	Address      string `json:"from"`
	KtoAddr        string `json:"ktoTo"`
	KtoAddrPrivate string `json:"ktoToPrivate"`
	TronAddr        string `json:"tronTo"`
	TronAddrPrivate string `json:"tronToPrivate"`
	IsTron          bool   `json:"isTron"`
	Symbol        string `json:"symbol"`
	Amount        uint64 `json:"amount"`
}

func (i FundCollectionData) MarshalBinary() ([]byte, error) {
	return json.Marshal(i)
}
