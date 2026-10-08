package tron

import (
	"bytes"
	"com.fibonacci.crowd/config"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gofiber/fiber/v2"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"net/http"
	"strconv"
)

var httpClient = http.Client{}

func CreateAccount() (pubKey string, privateKey string, err error) {
	url := config.EtcConfig.TronNetwork.Host + "/createAccount"
	response, err := httpClient.Post(url, "application/json", bytes.NewReader([]byte("")))
	if err != nil {
		log.Error("post err", err)
		return
	}
	if response.StatusCode != 200 {
		log.Error("status err", err)
		return
	}

	bs, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Error("read body err", err)
		return
	}

	var account fiber.Map
	err = json.Unmarshal(bs, &account)
	if err != nil {
		log.Error("unmarshal body err", err)
		return
	}

	address := account["address"].(map[string]interface{})
	pubKey = address["base58"].(string)
	privateKey = account["privateKey"].(string)
	return
}

func TransferTrxFee(fromPrivate string, to string) (txId string, err error) {
	url := config.EtcConfig.TronNetwork.Host + "/transferTrx"

	var request = make(fiber.Map)
	request["from"] = fromPrivate
	request["to"] = to
	request["amount"] = 5

	jsonReq, err := json.Marshal(request)
	if err != nil {
		log.Errorln("marshal json request err", err)
		return
	}

	response, err := httpClient.Post(url, "application/json", bytes.NewReader(jsonReq))
	if err != nil {
		log.Error("post err", err)
		return
	}
	if response.StatusCode != 200 {
		log.Error("TransferTrxFee status err", response.StatusCode)
		errMsg, errs := ioutil.ReadAll(response.Body)
		if errs != nil {
			log.Error("TransferTrxFee read txId err", err)
			return "", errs
		}
		err = errors.New(fmt.Sprintf("TransferTrxFee status err: %s", errMsg))
		return
	}
	txbs, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Error("TransferTrxFee read txId err", err)
		return
	}
	txId = string(txbs)
	return
}

func TransferUsdt(fromPrivate string ,to string, amount uint64) (txId string, err error) {
	url := config.EtcConfig.TronNetwork.Host + "/transfer"

	var request = make(fiber.Map)
	request["from"] = fromPrivate
	request["to"] = to
	request["amount"] = amount

	jsonReq, err := json.Marshal(request)
	if err != nil {
		log.Errorln("TransferUsdt marshal json request err", err)
		return
	}

	response, err := httpClient.Post(url, "application/json", bytes.NewReader(jsonReq))
	if err != nil {
		log.Error("TransferUsdt post err", err)
		return
	}
	if response.StatusCode != 200 {
		log.Error("TransferUsdt status err", response.StatusCode)
		errMsg, errs := ioutil.ReadAll(response.Body)
		if err != nil {
			log.Error("TransferUsdt read txId err", err)
			return "", errs
		}
		err = errors.New(fmt.Sprintf("TransferUsdt status err: %s", errMsg))
		return
	}
	txbs, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Error("TransferUsdt read txId err", err)
		return
	}
	txId = string(txbs)
	return
}

func GetUsdtBalance(address string) (amount uint64, err error) {
	url := config.EtcConfig.TronNetwork.Host + "/balanceOf"
	var request = make(fiber.Map)
	request["address"] = address

	jsonReq, err := json.Marshal(request)
	if err != nil {
		log.Errorln("marshal json request err", err)
		return
	}

	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(jsonReq))
	if err != nil {
		return
	}
	if resp.StatusCode != 200 {
		err = errors.New("response status not 200")
		return
	}
	bs, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}
	amountI, err := strconv.Atoi(string(bs))
	if err != nil {
		return
	}

	amount = uint64(amountI)
	return
}

func GetTrxBalance(address string) (amount uint64, err error) {
	url := config.EtcConfig.TronNetwork.Host + "/balanceOfTrx"
	var request = make(fiber.Map)
	request["address"] = address

	jsonReq, err := json.Marshal(request)
	if err != nil {
		log.Errorln("marshal json request err", err)
		return
	}

	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(jsonReq))
	if err != nil {
		return
	}
	if resp.StatusCode != 200 {
		err = errors.New("response status not 200")
		return
	}
	bs, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}
	amountI, err := strconv.Atoi(string(bs))
	if err != nil {
		return
	}

	amount = uint64(amountI)
	return
}


func GetTransactionInfo(hash string) (status string, err error) {
	url := config.EtcConfig.TronNetwork.Host + "/getTransactionByHash"
	var request = make(fiber.Map)
	request["hash"] = hash

	jsonReq, err := json.Marshal(request)
	if err != nil {
		log.Errorln("marshal json request err", err)
		return
	}

	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(jsonReq))
	if err != nil {
		return
	}
	if resp.StatusCode != 200 {
		err = errors.New("response status not 200")
		return
	}
	bs, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var data fiber.Map
	err = json.Unmarshal(bs, &data)
	if err != nil {
		return
	}
	statusRet := data["ret"]
	if statusRet != nil {
		status = statusRet.([]interface{})[0].(map[string]interface{})["contractRet"].(string)
	}
	return
}


