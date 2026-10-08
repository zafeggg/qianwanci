package main

import (
	"encoding/json"
	"fmt"
	"github.com/gofiber/fiber/v2"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"testing"
)

func TestTrx(t *testing.T) {
	tronAddres := "TWkWSSFjuntrK3y9UHcoX6odzesir51FJe"
	confirmReq := fmt.Sprintf("https://api.trongrid.io/v1/accounts/"+tronAddres+"/transactions/trc20?limit=1&contract_address=TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t&only_unconfirmed=false")
	resp, err := client.Get(confirmReq)
	if err != nil {
		log.Errorln("query accounts transactions error", err)
		return
	}
	bs, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Errorln("read accounts transactions body error", err)
		return
	}
	var result fiber.Map
	err = json.Unmarshal(bs, &result)
	if err != nil {
		log.Errorln("unmarshal body error", err)
		return
	}


	list := result["data"].([]interface {})
	for _, confirmed := range list {
		resp := confirmed.(map[string]interface {})
		from := resp["from"].(string)
		to := resp["to"].(string)
		hash := resp["transaction_id"].(string)
		value := resp["value"].(string)
		trxInfo := resp["token_info"].(map[string]interface {})
		symbol := trxInfo["symbol"].(string)

		fmt.Println(from, to, hash, value, symbol)
	}

}
