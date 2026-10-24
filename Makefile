VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
# 发布验签公钥（TODO 404）：deploy/picopanel.pub 的 hex 注入两端二进制，在线更新验签用
PUB_HEX := $(shell cat deploy/picopanel.pub 2>/dev/null)
ifeq ($(strip $(PUB_HEX)),)
PUB_HEX := empty
endif
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.releasePubKey=$(PUB_HEX)
GO ?= go
# 国内网络默认走 goproxy.cn；已有代理环境时不用覆盖
GOPROXY ?= https://goproxy.cn,direct
export GOPROXY

.PHONY: web server agent test run clean release release-notify build-notice channel publish release-gh \
	agent-linux agent-linux-amd64 agent-linux-arm64 server-linux-amd64

## 前端构建：产物输出到 internal/server/ui/dist 供服务端 embed（npm 缓存放项目内）。
## 两级跳过提速：node_modules 在位且比 package-lock 新 → 跳过 npm ci；
## web 源码指纹（src/public/配置/依赖清单）与上次构建一致且 dist 在位 → 跳过 vite
WEB_DEPS := web/package.json web/package-lock.json web/vite.config.js web/index.html web/scripts/copy-vendor.mjs
WEB_SRC := $(shell find web/src web/public -type f 2>/dev/null | sort)
WEB_HASH := $(shell cat $(WEB_DEPS) $(WEB_SRC) 2>/dev/null | git hash-object --stdin)
WEB_STAMP := release/.web-src.sha256

web:
	@if [ ! -d web/node_modules ] || [ web/package-lock.json -nt web/node_modules ]; then \
		echo "==> 安装前端依赖（npm ci）"; \
		cd web && (npm ci --cache .npm-cache || npm install --cache .npm-cache); \
	else \
		echo "==> 前端依赖无变化，跳过 npm ci"; \
	fi
	@mkdir -p $(dir $(WEB_STAMP))
	@if [ -f internal/server/ui/dist/index.html ] && [ -f $(WEB_STAMP) ] \
		&& [ "$(WEB_HASH)" = "$$(cat $(WEB_STAMP) 2>/dev/null)" ]; then \
		echo "==> Web 源码无变化，跳过 vite 构建（复用已有 dist）"; \
	else \
		echo "==> 构建前端（vite）"; \
		(cd web && npm run build) && printf '%s\n' '$(WEB_HASH)' > $(WEB_STAMP); \
	fi
	touch internal/server/ui/dist/.gitkeep   # vite emptyOutDir 会清掉占位文件，构建后补回

## 服务端（内嵌前端，单二进制）
server: web
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/lz-server ./cmd/lz-server

## agent（当前平台，macOS 上仅供调试，采集 /proc 不可用属预期）
agent:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/lz-agent ./cmd/lz-agent

test:
	$(GO) vet ./... && $(GO) test ./...

## 本机跑服务端（当前目录建库 lz-monitor.db，Ctrl+C 退出）
run: server
	./bin/lz-server

## Linux 交叉编译（纯 Go SQLite，无 CGO 依赖）
agent-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' \
		-o release/lz-agent-linux-amd64 ./cmd/lz-agent

agent-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' \
		-o release/lz-agent-linux-arm64 ./cmd/lz-agent

## agent 源码指纹（agent 可达的全部内部包 + go.mod/go.sum）。
## 与上次构建一致且两架构产物在位 → release 时跳过 agent 交叉编译，复用已有二进制
##（agent 的 version 字符串不随之刷新——功能无变化，已部署 agent 无需更新）。
## ⚠️ 新增 agent 可达的内部包必须同步加进这里（go list -deps ./cmd/lz-agent 核对）：
## 漏算=指纹不变复用旧二进制——v1.0.4 曾因漏 internal/{agent/collect,ptfetch} 把
## 未含修复的 agent 发上 GitHub（已重发）；B 站追更的 internal/bilibili 也一直漏算，
## 2026-10-23 补齐。
AGENT_SRC := $(shell find cmd/lz-agent internal/agent internal/agent/collect internal/bilibili internal/conf internal/logutil internal/proto internal/ptfetch internal/update \
	-name '*.go' -type f 2>/dev/null | sort)
# 指纹必须掺入 VERSION（git describe）：tag 变更=版本串变更，源码没动也必须重建——
# v1.0.5 实坑：构建在打 tag 前跑过一次（agent 烙了 v1.0.4-1-gXXX），publish 指纹无
# 变化跳过重建，旧版本串二进制进了 Release，agent 升级后自报旧版本、列表永远可升级
AGENT_HASH := $(shell { cat go.mod go.sum $(AGENT_SRC) 2>/dev/null; echo '$(VERSION)'; } | git hash-object --stdin)
AGENT_STAMP := release/.agent-src.sha256

## release 用：源码无变化时跳过两架构 agent 构建；单架构强制重建用上面的独立目标（不写指纹）
agent-linux:
	@if [ -f release/lz-agent-linux-amd64 ] && [ -f release/lz-agent-linux-arm64 ] \
		&& [ -f $(AGENT_STAMP) ] && [ "$(AGENT_HASH)" = "$$(cat $(AGENT_STAMP) 2>/dev/null)" ]; then \
		echo "==> agent 源码无变化，跳过 agent 构建（复用已有二进制）"; \
	else \
		echo "==> 构建 agent（linux/amd64 + linux/arm64）"; \
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' \
			-o release/lz-agent-linux-amd64 ./cmd/lz-agent && \
		CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' \
			-o release/lz-agent-linux-arm64 ./cmd/lz-agent && \
		printf '%s\n' '$(AGENT_HASH)' > $(AGENT_STAMP); \
	fi

server-linux-amd64: web
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' \
		-o release/lz-server-linux-amd64 ./cmd/lz-server

## release：交叉编译 Linux 产物并组好部署目录（不打包 tar）。产出：
##   release/deploy/server/       lz-server.gz + server.yaml + lz-server.sh → 上传到 /home/lz-monitor/server/
##   release/deploy/agent-amd64/  lz-agent.gz  + agent.yaml  + lz-agent.sh  → 上传到 /home/lz-monitor/agent/（x86_64）
##   release/deploy/agent-arm64/  同上（ARM64）
## 二进制以 gzip 压缩包分发（传输体积约为明文 40%），启停脚本 upgrade/start 时自动解压替换。
## gzip -9n 为确定性压缩：内容不变则 .gz 逐字节一致，rsync 增量传输不受影响。
## agent 与 server 分目录部署，互不混杂；脚本路径全部相对自身所在目录。
## agent 源码无变化时跳过 agent 编译（见 agent-linux），部署目录仍会用已有二进制组好。
release: agent-linux server-linux-amd64
	# 两端新版本判定（构建口令自动化，2026-10-17）：可达源码较上次构建时刻的
	# 变更文件数——server=internal/server+bilibili+web/src（内嵌前端），agent=
	# internal/agent+bilibili+ptfetch+proto（与 AGENT_SRC 同口径）；写入 .changed-* 供 build-notice 报告
	@if [ -f release/.last-release-stamp ]; then 		echo "$$(find internal/server internal/bilibili web/src -newer release/.last-release-stamp -not -name '.gitkeep' 2>/dev/null | wc -l | tr -d ' ')" > release/.changed-server; 		echo "$$(find internal/agent internal/bilibili internal/ptfetch internal/proto -newer release/.last-release-stamp 2>/dev/null | wc -l | tr -d ' ')" > release/.changed-agent; 	else 		echo unknown > release/.changed-server; echo unknown > release/.changed-agent; 	fi
	@mkdir -p release/deploy/server release/deploy/agent-amd64 release/deploy/agent-arm64
	# .gz 比二进制新（二进制未重建）则跳过重压缩——gzip -9n 对十几 MB 的包并不快
	@[ release/deploy/agent-amd64/lz-agent.gz -nt release/lz-agent-linux-amd64 ] \
		|| gzip -9n -c release/lz-agent-linux-amd64 > release/deploy/agent-amd64/lz-agent.gz
	@[ release/deploy/agent-arm64/lz-agent.gz -nt release/lz-agent-linux-arm64 ] \
		|| gzip -9n -c release/lz-agent-linux-arm64 > release/deploy/agent-arm64/lz-agent.gz
	@[ release/deploy/server/lz-server.gz -nt release/lz-server-linux-amd64 ] \
		|| gzip -9n -c release/lz-server-linux-amd64 > release/deploy/server/lz-server.gz
	for d in release/deploy/agent-amd64 release/deploy/agent-arm64; do \
		cp deploy/agent.example.yaml $$d/agent.yaml; \
		cp deploy/lz-agent.sh $$d/lz-agent.sh; \
		printf '%s\n' '$(VERSION)' > $$d/VERSION; \
	done
	cp deploy/server.example.yaml release/deploy/server/server.yaml
	cp deploy/lz-server.sh release/deploy/server/lz-server.sh
	printf '%s\n' '$(VERSION)' > release/deploy/server/VERSION
	chmod +x release/deploy/*/lz-agent.sh release/deploy/server/lz-server.sh
	@touch release/.last-release-stamp

## 发布报告（322）：release 后自动核对 deploy 差异/产物时间戳/tar 残留，并打印两套启停命令
## 「发布」口令固定收尾——输出即报告，转述即可，不再手工拼
release-notify: release
	@echo '=== 发布报告 ==='
	@echo '版本:' "$$(git describe --tags --always 2>/dev/null || echo dev)"
	@ls -l release/deploy/server/lz-server.gz release/deploy/agent-amd64/lz-agent.gz release/deploy/agent-arm64/lz-agent.gz 2>/dev/null | awk '{print $$6, $$7, $$8, " ", $$9}'
	@if [ -n "$$(git status --porcelain deploy/ 2>/dev/null)" ]; then \
		echo '脚本/配置有未提交改动（随本次发布已打包）:' && git status --short deploy/; \
	else \
		echo '脚本/配置无变化，沿用已部署的旧文件即可（deploy/ 最近一次提交: '$$(git log -1 --format='%h %ad' --date=short -- deploy/ 2>/dev/null)'）'; \
	fi
	@tarleft=$$(find . -maxdepth 2 -name '*.tar.gz' -not -path './web/*' 2>/dev/null); \
		if [ -n "$$tarleft" ]; then echo '⚠ tar 残留:' "$$tarleft"; else echo '无 tar 残留'; fi
	@echo ''
	@echo '## 服务端机（/home/lz-monitor/server/）——部署目录 release/deploy/server/'
	@for c in start stop restart status upgrade; do echo "/home/lz-monitor/server/lz-server.sh $$c"; done
	@echo ''
	@echo '## 被监控机（/home/lz-monitor/agent/，按架构选 agent-amd64 / agent-arm64 目录）'
	@echo '# agent 二进制未变时无需更新，命令备用：'
	@for c in start stop restart status upgrade; do echo "/home/lz-monitor/agent/lz-agent.sh $$c"; done

## build-notice（构建口令的固定输出）：结论行（含版本串）+ 两端新版本判定 +
## 命令块，我原样转贴整块即完成报告——判定由 release 目标自动计算（可达源码
## 较上次构建的变更文件数），不再手工 git diff/比对 mtime（省 token）。
## 时间戳取 gz 文件 mtime（含日期防跨天认错包），不取当前时间
build-notice:
	@printf '构建完成，lz-server.gz %s ｜ 版本串 %s\n' "$$(ls -l release/deploy/server/lz-server.gz | awk '{print $$6, $$7, $$8}')" "$$(cat release/deploy/server/VERSION 2>/dev/null)"
	@sc=$$(cat release/.changed-server 2>/dev/null || echo unknown); 	ac=$$(cat release/.changed-agent 2>/dev/null || echo unknown); 	if [ "$$sc" = "0" ]; then echo 'server 新版本：无（可达源码自上次构建无变更，沿用在跑包即可）'; else echo "server 新版本：有（可达源码变更 $$sc 个文件，需部署）"; fi; 	if [ "$$ac" = "0" ]; then echo 'agent 新版本：无（可达源码自上次构建无变更，沿用在跑包即可）'; else echo "agent 新版本：有（可达源码变更 $$ac 个文件，agent 需升级）"; fi
	@printf '\n## 服务端机（/home/lz-monitor/server/）——部署目录 release/deploy/server/\n'
	@for c in start stop restart status upgrade; do echo "/home/lz-monitor/server/lz-server.sh $$c"; done
	@echo
	@printf '## 被监控机（/home/lz-monitor/agent/，按架构选 agent-amd64 / agent-arm64 目录）\n'
	@for c in start stop restart status upgrade; do echo "/home/lz-monitor/agent/lz-agent.sh $$c"; done

## ── 发布通道（TODO 404，GitHub Releases 单源）────────────────────────────
## channel = 组包+签名（不发网）：三 .gz + manifest.json + .sig → release/channel/
## publish = channel 之后：tag 推 Codeup → gh 传 Release（draft→正式）→ 自验
## 前置：① HEAD 打了 annotated tag（先 tag 后构建：版本串与更新日志都取自它）；
##      ② 签名私钥在位（lz-sign genkey 生成，缺省 ~/.picopanel/release.key）；
##      ③ publish 另需公开发行仓 GH_REPO=<owner>/<repo> 与 gh 登录（或 GH_TOKEN）。
## beta 通道：make publish PUBLISH_CHANNEL=beta（Release 带 prerelease 标记）
RELEASE_KEY ?= $(HOME)/.picopanel/release.key
PUBLISH_CHANNEL ?= stable
GH_REPO ?=
CHANNEL_DIR := release/channel

channel: release
	@v=$$(git describe --tags --exact-match 2>/dev/null || true); \
	[ -n "$$v" ] || { echo '错误: HEAD 不在 tag 上——发布必须先打 tag（git tag -a vX.Y.Z -m "更新日志"，并推到 Codeup）'; exit 1; }; \
	[ -f "$(RELEASE_KEY)" ] || { echo "错误: 缺签名私钥 $(RELEASE_KEY)（go run ./cmd/lz-sign genkey 生成，私钥放仓库外）"; exit 1; }; \
	GO="$(GO)" deploy/channel.sh "$$v" "$(PUBLISH_CHANNEL)" "$(CHANNEL_DIR)" "$(RELEASE_KEY)" deploy/picopanel.pub

publish: channel
	@v=$$(git describe --tags --exact-match 2>/dev/null); \
	command -v gh >/dev/null 2>&1 || { echo '错误: 未安装 gh CLI（brew install gh）'; exit 1; }; \
	[ -n "$(GH_REPO)" ] || { echo '错误: 缺 GH_REPO（公开发行仓），如: make publish GH_REPO=yourname/picopanel-release'; exit 1; }; \
	gh auth status >/dev/null 2>&1 || [ -n "$${GH_TOKEN:-}" ] || { echo '错误: gh 未登录且未设 GH_TOKEN（gh auth login 或 export GH_TOKEN=…）'; exit 1; }; \
	echo "==> 推 tag $$v 到 Codeup"; \
	git push origin "$$v"; \
	pre=""; [ "$(PUBLISH_CHANNEL)" = beta ] && pre="--prerelease"; \
	if gh release view "$$v" --repo "$(GH_REPO)" >/dev/null 2>&1; then \
		echo "==> Release $$v 已存在，跳过创建（续传资产）"; \
	else \
		gh release create "$$v" --repo "$(GH_REPO)" --target main --draft $$pre \
			--title "PicoPanel $$v" --notes-file "$(CHANNEL_DIR)/notes.txt"; \
	fi; \
	gh release upload "$$v" --repo "$(GH_REPO)" --clobber \
		"$(CHANNEL_DIR)/lz-server.gz" "$(CHANNEL_DIR)/lz-agent-amd64.gz" "$(CHANNEL_DIR)/lz-agent-arm64.gz" \
		"$(CHANNEL_DIR)/manifest.json" "$(CHANNEL_DIR)/manifest.json.sig"; \
	gh release edit "$$v" --repo "$(GH_REPO)" --draft=false $$pre; \
	echo "==> 自验: 拉线上 manifest 比对并验签"; \
	curl -fsSL "https://github.com/$(GH_REPO)/releases/download/$$v/manifest.json" -o "$(CHANNEL_DIR)/manifest.remote.json"; \
	cmp -s "$(CHANNEL_DIR)/manifest.json" "$(CHANNEL_DIR)/manifest.remote.json" || { echo '错误: 线上 manifest 与本地不一致'; exit 1; }; \
	"$(GO)" run ./cmd/lz-sign verify -pub deploy/picopanel.pub -i "$(CHANNEL_DIR)/manifest.remote.json" -s "$(CHANNEL_DIR)/manifest.json.sig" >/dev/null; \
	echo "==> 发布完成: $$v（$(PUBLISH_CHANNEL)）已上线并通过自验"

## 一条龙「发布到 github」（2026-10-06 简化）：清洁树检查 → 自动升小版本（可
## RELEASE_VERSION= 点名；勿用 VERSION——那是构建 describe 变量、恒非空，v1.0.6
## 前夜实坑：它顶掉自动升版，把 v1.0.5-1-gXXX 发布了出去）→ git log 起草更新
## 日志（可 NOTES=文件 手写覆盖）→ tag+推 Codeup → publish（组包/签名/上传
## Releases/自验）。用法:
##   make release-gh GH_REPO=lyxizq/picopanel-release
##   make release-gh GH_REPO=lyxizq/picopanel-release NOTES=.tmp/notes.txt PUBLISH_CHANNEL=beta
release-gh:
	@command -v gh >/dev/null 2>&1 || { echo '错误: 未安装 gh CLI'; exit 1; }; \
	[ -n "$(GH_REPO)" ] || { echo '错误: 缺 GH_REPO（GH_REPO=owner/picopanel-release）'; exit 1; }; \
	[ -f "$(RELEASE_KEY)" ] || { echo "错误: 缺签名私钥 $(RELEASE_KEY)"; exit 1; }; \
	[ -z "$$(git status --porcelain)" ] || { echo '错误: 工作区有未提交改动——发布 tag 必须指向含全部发布内容的提交，先提交再发'; exit 1; }; \
	mkdir -p .tmp; \
	old=$$(git describe --tags --abbrev=0); \
	if [ -n "$(RELEASE_VERSION)" ]; then v="$(RELEASE_VERSION)"; else v=$$(echo "$$old" | sed 's/^v//' | awk -F. '{printf "v%d.%d.%d", $$1, $$2, $$3+1}'); fi; \
	if [ -n "$(NOTES)" ] && [ -f "$(NOTES)" ]; then cp "$(NOTES)" .tmp/release-gh-notes.txt; \
	else { echo "$$v"; echo; echo "更新日志（git log 自动起草；手写用 NOTES=文件 覆盖）:"; git log --pretty='- %s' "$$old..HEAD" | cut -c1-200; } > .tmp/release-gh-notes.txt; fi; \
	echo "==> $$old → $$v（$(PUBLISH_CHANNEL)），更新日志预览："; head -6 .tmp/release-gh-notes.txt; \
	git tag -a "$$v" -F .tmp/release-gh-notes.txt && \
	git push origin main && \
	$(MAKE) publish GH_REPO=$(GH_REPO) PUBLISH_CHANNEL=$(PUBLISH_CHANNEL)

clean:
	rm -rf bin release
	rm -rf internal/server/ui/dist/*
	touch internal/server/ui/dist/.gitkeep
