# 千万次（TiMi）常用命令入口（2026-09-30 新增）
#
# 为什么要有 Makefile：仓库原有的构建入口只有 Windows 批处理（build-all.bat / build-linux.bat），
# Linux/macOS 上无法一键构建或自检；CI 也缺少一个"和文档一致"的命令入口。
#
# 说明：
# - 本机离线开发时，依赖在 `<仓库根>/.gomodcache`（仓库自带），因此这里**默认强制使用工作区缓存**，
#   不依赖外网；需要联网拉依赖时显式 `make auth` 传 GOFLAGS=-mod=mod 或清空 GOMODCACHE。
# - 私链联调/E2E 需要 hardhat 节点 + MySQL + Redis，见 scripts/*.ps1（本 Makefile 只做静态与单测）。
#
# 常用：
#   make check        后端编译 + 静态检查 + 单测（CI 同款）
#   make bintest      产出 Linux 静态二进制到 build/linux/
#   make frontend     前端单测 + 构建
#   make all          check + frontend

SHELL := /bin/sh
ROOT  := $(CURDIR)
BACK  := $(ROOT)/houduan/TiMi
FRONT := $(ROOT)/dapp-vue

# 工作区内 Go 缓存（依赖已随仓库落地，不需要外网）
export GOMODCACHE ?= $(ROOT)/.gomodcache
export GOCACHE    ?= $(ROOT)/.gocache
export GOPATH     ?= $(ROOT)/.gopath
export GOFLAGS    ?= -p=1
export GOPROXY    ?= off

# 生产必需的六个进程（见 千万次-生产部署清单.md §1）
BINS := npower round evmwatch hashpower burn trshash
# cmd/ 下的包路径（npower 在 cmd 根）
pkg_of = $(if $(filter npower,$(1)),./cmd,./cmd/$(1))

.PHONY: all check backend-build backend-vet backend-test bintest frontend frontend-test frontend-build e2e-help clean

all: check frontend

## check 后端编译 + vet + 单测（与 CI 一致）
check: backend-build backend-vet backend-test
	@echo "✅ 后端 check 通过"

backend-build:
	@echo "== go build ./... =="
	cd $(BACK) && go build ./...

backend-vet:
	@echo "== go vet ./... =="
	cd $(BACK) && go vet ./...

## 集成用例默认跳过（需 NPOWER_TEST_CONFIG + MySQL/Redis）；纯逻辑单测会执行
backend-test:
	@echo "== go test ./... =="
	cd $(BACK) && go test -count=1 ./...

## bintest 交叉编译 Linux 静态二进制（生产产物）
bintest:
	@mkdir -p $(BACK)/build/linux
	@for b in $(BINS); do \
		echo "== 构建 $$b =="; \
		cd $(BACK) && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" \
			-o build/linux/$$b $(call pkg_of,$$b) || exit 1; \
	done
	@echo "✅ 产物在 houduan/TiMi/build/linux/"

frontend: frontend-test frontend-build

## 前端纯逻辑单测（node:test，无额外依赖）
frontend-test:
	@echo "== npm test (node --test) =="
	cd $(FRONT) && npm test

## 前端生产构建（必须显式注入后端域名，否则产物是占位域名）
frontend-build:
	@echo "== npm run build =="
	cd $(FRONT) && npm run build

## e2e-help 私链端到端需要外部依赖，这里只提示
e2e-help:
	@echo "私链 E2E 需要：hardhat 节点(:8545) + MySQL + Redis + npower/ops/round 常驻"
	@echo "  1) contracts:  npm run node:local        （终端 A 常驻）"
	@echo "  2) contracts:  npm run prepare:localnode"
	@echo "  3) houduan/TiMi: build\\npower.exe -f config\\etc.privchain.yml  （以及 ops / round）"
	@echo "  4) powershell -File scripts/privchain-e2e.ps1"
	@echo "  5) powershell -File scripts/privchain-fix-verify.ps1   （修复项回归，PASS=25/FAIL=0）"

## clean 只清构建产物，不动工作区缓存（.gomodcache/.gocache 很大，别误删）
clean:
	rm -rf $(BACK)/build
	rm -rf $(FRONT)/dist
