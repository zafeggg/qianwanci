# 千万次（TiMi）众筹系统 — 部署说明

> 品牌：千万次（TiMi）｜基线：crowd-master（Npower）复用改造
> 本地一键启动：双击 `start-local.bat`（自动起 MySQL80/Redis → npower :3000 → demo 演示网关 :3001，页面 http://127.0.0.1:3001）
> 以下为历史测试环境部署命令（旧测试服 154.85.x，生产部署清单另出）


#### 测试环境部署
```text
1.主程序

cd ./cmd/  
GO111MODULE="on" CGO_ENABLED=0 GOOS=linux GOARCH=amd64  go build -v -o npower main.go  
scp ./npower root@154.85.61.43:/root/npower/mainnet/npowerRunT    
nohup ./npowerRun -f ./etc.yml >crowd.log 2>&1 &  

```

```text
2.开始结束项目轮程序

cd ./cmd/round/  
GO111MODULE="on" CGO_ENABLED=0 GOOS=linux GOARCH=amd64  go build -v -o round round.go  
scp ./round root@154.85.62.206:/root/npower/round/roundRunT  
nohup ./roundRun -f ./round.yml >round.log 2>&1 &  

```


```text
3.充币到账记录监听程序

cd ./cmd/trshash/  
GO111MODULE="on" CGO_ENABLED=0 GOOS=linux GOARCH=amd64  go build -v -o trshash trshash.go  
scp ./trshash root@154.85.62.206:/root/npower/trshash/trshashRunT  
nohup ./trshashRun -f ./trs.yml >trs.log 2>&1 &  

```

```text
4.归集代币

cd ./cmd/collecting/  
GO111MODULE="on" CGO_ENABLED=0 GOOS=linux GOARCH=amd64  go build -v -o collecting collecting.go  
scp ./collecting  root@154.85.62.206:/root/npower/collecting/collectingT  
nohup ./collecting -f ./collecting.yml >collecting.log 2>&1 &  
```



```text
5.挖矿收益定时器程序

cd ./cmd/mining/  
GO111MODULE="on" CGO_ENABLED=0 GOOS=linux GOARCH=amd64  go build -v -o mining mining.go  
scp ./mining root@154.85.61.43:/root/npower/mining/miningRunT  
nohup ./miningRun -f ./mining.yml -net http://192.168.0.5:6000 > mining.log 2>&1 &  

```


```text
6.运维程序

cd ./cmd/ops/  
GO111MODULE="on" CGO_ENABLED=0 GOOS=linux GOARCH=amd64  go build -v -o ops ops.go  
scp ./ops root@154.85.61.43:/root/npower/ops/opsRunT 
mv opsRunT opsRun   
nohup ./opsRun -f ./ops.yml >ops.log 2>&1 &  


6.手动归集

cd ./cmd/manualcollection/  
GO111MODULE="on" CGO_ENABLED=0 GOOS=linux GOARCH=amd64  go build -v -o manual manual.go  
scp ./manual root@154.85.61.43:/root/npower/manual/manualT 
mv manualT manual   
nohup ./manual -f ./manual.yml -symbol FIBO >manual.log 2>&1 &  

```

##### 正式环境部署
```text




```