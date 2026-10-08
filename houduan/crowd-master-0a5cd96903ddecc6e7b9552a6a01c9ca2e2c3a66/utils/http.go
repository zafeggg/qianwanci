package utils

import "encoding/json"

type XResponse struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data,omitempty"`
}

func JsonOk(data interface{}) []byte {
	r := XResponse{
		Code: 200,
		Msg:  "success",
		Data: data,
	}

	bs, err := json.Marshal(r)
	if err != nil {
		return nil
	}
	return bs
}

func JsonFail(msg string) []byte {
	r := XResponse{
		Code: 600,
		Msg:  msg,
		Data: nil,
	}

	bs, err := json.Marshal(r)
	if err != nil {
		return nil
	}
	return bs
}


func JsonFailCode(code int, msg string) []byte {
	r := XResponse{
		Code: code,
		Msg:  msg,
		Data: nil,
	}

	bs, err := json.Marshal(r)
	if err != nil {
		return nil
	}
	return bs
}