package handler

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/crypto"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/utils"
	"errors"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v4"
	log "github.com/sirupsen/logrus"
	"strings"
	"time"
)

func GetWalletId(c *fiber.Ctx) (uint, error) {
	user := c.Locals("user").(*jwt.Token)
	claims := user.Claims.(jwt.MapClaims)
	var wi interface{}
	var ok bool
	if wi, ok = claims["walletId"]; !ok {
		return 0, errors.New("wallet id not exist")
	}
	walletId := uint(wi.(float64))

	//check token
	bearerToken := c.Get("Authorization")
	if bearerToken == "" {
		return 0, errors.New("authorization not exist")
	}
	tokens := strings.SplitAfter(bearerToken, "Bearer ")
	if len(tokens) <= 0 {
		return 0, errors.New("bearer split less 2")
	}
	token := tokens[1]
	if token == "" {
		return 0, errors.New("token not exist")
	}
	err := utils.CheckToken(walletId, token)
	if err != nil {
		return 0, err
	}

	return walletId, nil
}


func DecryptData(c *fiber.Ctx) (data []byte, err error) {
	log.Infoln(c.Context().UserValue(enum.AesSignContextKey))
	key := c.Context().UserValue(enum.AesSignContextKey).(string)
	var encryptData = c.Context().PostBody()

	if data, err = crypto.AesDecrypt(encryptData, []byte(key)); err != nil {
		log.Error("decryptData aesDecrypt: ", err)
		return
	}
	if data == nil {
		log.Error("decryptData aesDecrypt empty: ", err)
		return
	}
	fmt.Println("data: ", string(data))
	return
}


func GenerateToken(walletId uint, name string, exp time.Duration) (string, error) {
	// Set claims
	claims := jwt.MapClaims{}
	claims["walletId"] = walletId
	claims["name"] = name
	claims["exp"] = time.Now().Add(exp).Unix()

	// Create token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// Generate encoded token and send it as response.
	t, err := token.SignedString([]byte(config.EtcConfig.SecretKey))
	if err != nil {
		return "", err
	}
	return t, nil
}
