package utils

import (
	"com.fibonacci.crowd/config"
	"context"
	"errors"
	"github.com/go-redis/redis/v8"
	"strconv"
	"time"
)

func LoginToken(walletId uint, token string, expiration time.Duration) error {
	err := config.RedisClient.Set(context.Background(), "Login_"+strconv.Itoa(int(walletId)), token, expiration).Err()
	if err != nil {
		return err
	}
	return nil
}

func CheckToken(walletId uint, token string) error {
	tokenReal, err := config.RedisClient.Get(context.Background(), "Login_"+strconv.Itoa(int(walletId))).Result()
	switch err {
	case nil:
	case redis.Nil:
		return errors.New("InvalidTokenWallet")
	default:
		return errors.New("InvalidTokenWallet")
	}

	if tokenReal != token {
		return errors.New("InvalidTokenWallet")
	}
	return nil
}
