# 交易所对接文档

## 0、数据类型定义
- Tx
    | 序号  | 字段      | 类型   | 描述         |
    | :---: | :-------- | :----- | :----------- |
    |   1   | Nonce     | uint64 | 随机数       |
    |   2   | BlockNum  | uint64 | 块号         |
    |   3   | Amount    | uint64 | 金额         |
    |   4   | From      | string | 发送者地址   |
    |   5   | To        | string | 接收者地址   |
    |   6   | Hash      | string | 交易hash     |
    |   7   | Signature | string | 交易签名     |
    |   8   | Time      | int64  | 时间戳       |
    |   9   | Script    | string | 代币协议     |
    |  10   | Fee       | uint64 | 手续费       |
    |  11   | Root      | bytes  | 根           |
    |  12   | Tag       | int32  | 交易类型标志 |
- hashMsg
    | 序号  | 字段    | 类型   | 描述                       |
    | :---: | :------ | :----- | :------------------------- |
    |   1   | code    | int64  | 状态码，0表示成功          |
    |   2   | message | string | 错误消息                   |
    |   3   | hash    | string | hash值，code非零情况下为空 |
- block
    | 序号  | 字段          | 类型   | 描述         |
    | :---: | :------------ | :----- | :----------- |
    |   1   | Height        | uint64 | 块高         |
    |   2   | PrevBlockHash | string | 上个块的hash |
    |   3   | Txs           | []Tx   | 交易数据     |
    |   4   | Root          | string | 根数据       |
    |   5   | Version       | uint64 | 版本号       |
    |   6   | Timestamp     | int64  | 时间戳       |
    |   7   | Hash          | string | 块hash       |
    |   8   | Miner         | string | 矿工地址     |
- req_signed_transaction 
    | 序号  | 字段      | 类型   | 描述       |
    | :---: | :-------- | :----- | :--------- |
    |   1   | from      | string | 发送者地址 |
    |   2   | to        | string | 接收者地址 |
    |   3   | amount    | uint64 | 金额       |
    |   4   | nonce     | uint64 | 随机数     |
    |   5   | time      | int64  | 时间戳，秒 |
    |   5   | hash      | bytes  | hash值     |
    |   6   | signature | bytes  | 签名       |
- req_token_transaction
    | 序号  | 字段        | 类型   | 描述       |
    | :---: | :---------- | :----- | :--------- |
    |   1   | from        | string | 发送者地址 |
    |   2   | to          | string | 接收者地址 |
    |   3   | amount      | uint64 | 金额       |
    |   4   | nonce       | uint64 | 随机数     |
    |   5   | priv        | string | 私钥       |
    |   6   | tokenAmount | uint64 | 代币金额   |
    |   7   | symbol      | string | 代币名称   |
    |   8   | fee         | uint64 | 手续费     |
    |   9   | time        | int64  | 时间戳，秒 |
    |  10   | hash        | bytes  | 哈希值     |
    |  11   | signature   | bytes  | 签名       |
- req_transaction
     | 序号  | 字段   | 类型   | 描述       |
     | :---: | :----- | :----- | :--------- |
     |   1   | From   | string | 发送方地址 |
     |   2   | To     | string | 接收方地址 |
     |   3   | Amount | uint64 | 金额       |
     |   4   | Nonce  | uint64 | 随机数     |
     |   5   | Priv   | string | 私钥       |
## 1、发送转账交易
**接口定义**
```
rpc SendSignedTransactions(req_signed_transactions) returns(resp_signed_transactions) {}
```

- 请求参数
    | 序号  | 字段 | 类型                     | 描述     |
    | :---: | :--- | :----------------------- | :------- |
    |   1   | txs  | []req_signed_transaction | 交易列表 |
- 响应参数
    | 序号  | 字段     | 类型      | 描述     |
    | :---: | :------- | :-------- | :------- |
    |   1   | hashList | []hashMsg | 哈希信息 |

## 2、发送代币交易
**接口定义**
```
rpc SendSignedToken(req_token_transactions) returns(resp_signed_transactions) {}
```

- 请求参数 req_token_transactions
    | 序号  | 字段 | 类型                    | 描述         |
    | :---: | :--- | :---------------------- | :----------- |
    |   1   | txs  | []req_token_transaction | 代币交易列表 |
- 响应参数 resp_signed_transactions
    | 序号  | 字段     | 类型    | 描述     |
    | :---: | :------- | :------ | :------- |
    |   1   | hashList | hashMsg | hash信息 |

## 3、获取余额
**接口定义**
```
rpc GetBalance(req_balance) returns(res_balance) {}
```

- 请求参数 req_balance
    | 序号  | 字段    | 类型   | 描述 |
    | :---: | :------ | :----- | :--- |
    |   1   | address | string | 地址 |
  
- 响应参数 res_balance
    | 序号  | 字段   | 类型   | 描述         |
    | :---: | :----- | :----- | :----------- |
    |   1   | balnce | uint64 | 余额，精度11 |
## 4、获取代币余额
**接口定义**
```
rpc GetBalanceToken(req_token_balance) return (resp_token_balance) {}
```

- 请求参数 req_token_balance
    | 序号  | 字段    | 类型   | 描述   |
    | :---: | :------ | :----- | :----- |
    |   1   | address | string | 地址   |
    |   2   | symbol  | string | 代币名 |
  
- 响应参数 resp_token_balance
    | 序号  | 字段   | 类型   | 描述 |
    | :---: | :----- | :----- | :--- |
    |   1   | balnce | uint64 | 余额 |
    |   2   | demic  | uint64 | 精度 |

## 5、获取nonce
**接口定义**
```
  rpc GetAddressNonceAt(req_nonce) returns(respose_nonce) {}
```

- 请求参数 req_nonce
    | 序号  |  字段   | 类型   | 描述 |
    | :---: | :-----: | :----- | :--- |
    |   1   | address | string | 地址 |
    
  
- 响应参数 respose_nonce
    | 序号  | 字段  | 类型   | 描述    |
    | :---: | :---- | :----- | :------ |
    |   1   | nonce | uint64 | nonce值 |

## 6、通过hash获取交易
**接口定义**
```
  rpc GetTxByHash(req_tx_by_hash) returns(resp_tx_by_hash) {}
```

- 请求参数 req_tx_by_hash
    | 序号  | 字段 | 类型   | 描述   |
    | :---: | :--- | :----- | :----- |
    |   1   | hash | string | hash值 |
   
- 响应参数 resp_tx_by_hash
    | 序号  | 字段    | 类型   | 描述     |
    | :---: | :------ | :----- | :------- |
    |   1   | code    | int32  | 状态码   |
    |   2   | message | string | 错误消息 |
    |   3   | data    | Tx     | 交易数据 |

## 7、获取最大块号
**接口定义**
```
  rpc GetMaxBlockNumber(req_max_block_number) return (resp_max_block_number) {}
```

- 请求参数 req_max_block_number
    | 序号  | 字段 | 类型 | 描述 |
    | :---: | :--- | :--- | :--- |
  
- 响应参数 resp_max_block_number
    | 序号  | 字段      | 类型   | 描述 |
    | :---: | :-------- | :----- | :--- |
    |   1   | maxNumber | uint64 |      |

## 8、通过块号获取块数据
**接口定义**
```
  rpc GetBlockByNum(req_block_by_number) returns(resp_block) {}
```

- 请求参数 req_block_by_number
    | 序号  | 字段   | 类型   | 描述 |
    | :---: | :----- | :----- | :--- |
    |   1   | height | uint64 | 块高 |
- 响应参数 resp_block
    | 序号  | 字段    | 类型   | 描述     |
    | :---: | :------ | :----- | :------- |
    |   1   | code    | int32  | 状态码   |
    |   2   | message | string | 错误消息 |
    |   3   | data    | block  | 块数据   |

## 9、线上转账交易
**接口定义**
```
  rpc SendTransactions(req_transactions) return (resp_transactions) {}
```

- 请求参数 req_block_by_number
    | 序号  | 字段              | 类型     | 描述 |
    | :---: | :---------------- | :------- | :--- |
    |  txs  | []req_transaction | 交易数据 |
- 响应参数 resp_block
    | 序号  | 字段     | 类型      | 描述     |
    | :---: | :------- | :-------- | :------- |
    |   1   | hashList | []hashMsg | hash消息 |

## 10、线上创建地址
**接口定义**
```
rpc CreateAddr(req_create_addr) returns(resp_create_addr) {}
```

- 请求参数 req_create_addr

  
- 响应参数 resp_create_addr
    | 序号  | 字段    | 类型   | 描述 |
    | :---: | :------ | :----- | :--- |
    |   1   | address | string | 地址 |
    |   2   | privkey | string | 私钥 |

