package kto

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/kortho/transaction"
	"com.fibonacci.crowd/kortho/types"
	ktoutil "com.fibonacci.crowd/kortho/util"
	"context"
	"fmt"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"sync"
	"time"
)

var conns = make([]*grpc.ClientConn, 0)
var chnnChan = make(chan *grpc.ClientConn, 3)
var initOnce sync.Once

func NewClient() GreeterClient {
	ensureInit()
	conn := <-chnnChan
	c := NewGreeterClient(conn)
	return c
}

// ktoGrpcAddr 取当前生效的 KTO 链 gRPC 节点地址。
// 生产必须以 yml Chain.KtoGrpcAddr 配置；未配置时回落历史默认节点并告警。
func ktoGrpcAddr() string {
	if config.EtcConfig != nil && config.EtcConfig.Chain.KtoGrpcAddr != "" {
		return config.EtcConfig.Chain.KtoGrpcAddr
	}
	logrus.Warnln("[kto] 未配置 Chain.KtoGrpcAddr，回落历史默认节点", config.DefaultKtoGrpcAddr)
	return config.DefaultKtoGrpcAddr
}

// ensureInit 惰性建立连接池（sync.Once）。
// 原实现放在包 init() 中，包加载即拨号，早于 config.Init（yml 尚未读取），
// 导致节点地址无法配置；改为首次 NewClient 时初始化——各程序 main 均先 config.Init 再触链。
func ensureInit() {
	initOnce.Do(func() {
		address := ktoGrpcAddr()
		logrus.Infoln("[kto] 初始化 gRPC 连接池 ->", address)
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
	})
}

// ChainReachable 探测 KTO 链节点是否可达（供 /health 使用，不阻塞主流程）。
func ChainReachable(timeout time.Duration) bool {
	if config.EtcConfig == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, ktoGrpcAddr(), grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
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
// 说明（2026-09-30 清理）：原 KTOonChain 与下方 KTOonChainSync 实现完全重复且全仓零引用，已删除；
// 调用方统一用 KTOonChainSync（会等待链上回执，语义更明确）。

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
