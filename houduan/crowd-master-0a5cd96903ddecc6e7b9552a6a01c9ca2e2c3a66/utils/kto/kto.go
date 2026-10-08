package kto

import (
	"com.fibonacci.crowd/kortho/transaction"
	"com.fibonacci.crowd/kortho/types"
	ktoutil "com.fibonacci.crowd/kortho/util"
	"context"
	"fmt"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"time"
)

var conns = make([]*grpc.ClientConn, 0)
var chnnChan = make(chan *grpc.ClientConn, 3)

func NewClient() GreeterClient {
	// Set up a connection to the server.
	//initKTOGrpc()

	conn := <-chnnChan
	c := NewGreeterClient(conn)
	return c
}

func init() {
	address := "106.12.94.134:8545"
	for i := 0; i < 10; i++ {
		conn, _ := grpc.Dial(address, grpc.WithInsecure())
		conns = append(conns, conn)
	}
	go func() {
		for {
			for _, conn := range conns {

				if conn.GetState() == connectivity.Ready {
					chnnChan <- conn
					continue
				}
				if conn.GetState() == connectivity.Shutdown {
					conn, _ = grpc.Dial(address, grpc.WithInsecure())
				}
			}
		}
	}()
}

func GetBalance(symbol string, from string) (amount uint64) {
	if symbol == "KTO" {
		ktoBalance, err := NewClient().GetBalance(context.Background(), &ReqBalance{Address: from})
		if err != nil {
			logrus.Error("GetBalance err: ", err)
			return 0
		}
		return ktoBalance.Balnce
	} else {
		tokenBalance, err := NewClient().GetBalanceToken(context.Background(), &ReqTokenBalance{Address: from, Symbol: symbol})
		if err != nil {
			logrus.Error("GetBalance symbol err: ", err)
			return 0
		}
		return tokenBalance.Balnce
	}
}

func KTOonChain(symbol string, from string, fromPri string, to string, amount uint64) (nonce uint64, hash string, err error) {
	//每次消费数据 准备上链之前, 获取最新的Nonce
	nonce, err = getKtoNonce(from)
	if err != nil {
		return 0, "", err
	}
	if symbol == "KTO" {
		request := new(ReqTransactions)
		requestOne := &ReqTransaction{
			From:   from,
			To:     to,
			Amount: amount,
			Nonce:  nonce,
			Priv:   fromPri,
		}
		request.Txs = append(request.Txs, requestOne)
		response, err := NewClient().SendTransactions(context.Background(), request)
		if err != nil {
			return 0, "", err
		}
		hash = response.HashList[0].Hash
	} else {
		fromIn, _ := types.StringToAddress(from)
		toIn, _ := types.StringToAddress(to)
		tx := transaction.Transaction{
			Nonce:     nonce,
			Amount:    500000,
			From:      *fromIn,
			To:        *toIn,
			Hash:      nil,
			Signature: nil,
			Time:      time.Now().Unix(),
		}
		tx.HashTransaction()
		pri := ktoutil.Decode(fromPri)
		if err := tx.Sign(pri); err != nil {
			return 0, "", err
		}

		requestToken := &ReqTokenTransaction{
			From:        from,
			To:          to,
			Amount:      tx.Amount,
			Nonce:       nonce,
			Time:        tx.Time,
			Hash:        tx.Hash,
			Signature:   tx.Signature,
			Fee:         5000000,
			Symbol:      symbol,
			TokenAmount: amount,
		}
		request := []*ReqTokenTransaction{requestToken}
		response, err := NewClient().SendSignedToken(context.Background(), &ReqTokenTransactions{Txs: request})
		if err != nil {
			return 0, "", err
		}
		hash = response.HashList[0].Hash
	}
	return
}

func KTOonChainSync(symbol string, from string, fromPri string, to string, amount uint64) (nonce uint64, hash string, err error) {
	//每次消费数据 准备上链之前, 获取最新的Nonce
	nonce, err = getKtoNonce(from)
	if err != nil {
		return 0, "", err
	}
	if symbol == "KTO" {
		request := new(ReqTransactions)
		requestOne := &ReqTransaction{
			From:   from,
			To:     to,
			Amount: amount,
			Nonce:  nonce,
			Priv:   fromPri,
		}
		request.Txs = append(request.Txs, requestOne)
		response, err := NewClient().SendTransactions(context.Background(), request)
		if err != nil {
			return 0, "", err
		}
		hash = response.HashList[0].Hash
		if err = checkTransactionStatus(hash); err != nil {
			return 0, "", err
		}
	} else {
		fromIn, _ := types.StringToAddress(from)
		toIn, _ := types.StringToAddress(to)
		tx := transaction.Transaction{
			Nonce:     nonce,
			Amount:    500000,
			From:      *fromIn,
			To:        *toIn,
			Hash:      nil,
			Signature: nil,
			Time:      time.Now().Unix(),
		}
		tx.HashTransaction()
		pri := ktoutil.Decode(fromPri)
		if err := tx.Sign(pri); err != nil {
			return 0, "", err
		}

		requestToken := &ReqTokenTransaction{
			From:        from,
			To:          to,
			Amount:      tx.Amount,
			Nonce:       nonce,
			Time:        tx.Time,
			Hash:        tx.Hash,
			Signature:   tx.Signature,
			Fee:         5000000,
			Symbol:      symbol,
			TokenAmount: amount,
		}
		request := []*ReqTokenTransaction{requestToken}
		response, err := NewClient().SendSignedToken(context.Background(), &ReqTokenTransactions{Txs: request})
		if err != nil {
			return 0, "", err
		}
		hash = response.HashList[0].Hash
		if err = checkTransactionStatus(hash); err != nil {
			return 0, "", err
		}
	}
	return
}

func CreateWallet() (walletAddr string, walletPrivate string, err error) {
	response, err := NewClient().CreateAddr(context.Background(), &ReqCreateAddr{})
	if err != nil {
		return
	}
	walletAddr = response.Address
	walletPrivate = response.Privkey
	return
}

// 确保上链成功 或着失败
func checkTransactionStatus(hash string) (err error) {
	var checkFinishTimes = 0
	//时间兜底
	var checkTimeSec float64
	var checkHashResult *RespTxByHash

	beginCheckTime := time.Now()
	for checkTimeSec < 10 {
		checkHashResult, err = NewClient().GetTxByHash(context.Background(), &ReqTxByHash{Hash: hash})
		if err != nil {
			logrus.Error("CheckHash failed: ", err)
			return
		}

		logrus.WithFields(logrus.Fields{"CheckHash result code": checkHashResult.Code,
			"message": checkHashResult.Message}).Info("hash checking: ", hash)

		//0上链成功 1上链中 -1未上链/交易还没散播出去
		if checkHashResult.Code == 0 {
			break
		} else {
			checkFinishTimes++
		}

		time.Sleep(1 * time.Second)
		checkTimeSec = time.Since(beginCheckTime).Seconds()
	}

	//上链成功
	if checkHashResult.Code == 0 {
		return nil
	}

	//上链失败
	return fmt.Errorf("检测上链失败, result code: %d", checkHashResult.Code)
}

func getKtoNonce(addr string) (uint64, error) {
	var request ReqNonce
	request.Address = addr

	response, err := NewClient().GetAddressNonceAt(context.Background(), &request)
	if err != nil {
		return 1, err
	}
	return response.Nonce, nil
}
