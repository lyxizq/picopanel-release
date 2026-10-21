# PicoPanel

Linux 服务器监控系统：Go + SQLite 单二进制服务端（内嵌 Web 前端）+ 零依赖采集 agent，支持 Agent 拉取 / Agent 上报 / node_exporter 三种采集方式，带 Web SSH 终端、文件管理（SFTP）、阈值告警推送、历史曲线等模块。

- **下载安装**：[Releases](../../releases)（`lz-server.gz` / `lz-agent-amd64.gz` / `lz-agent-arm64.gz`，附 sha256 清单与 ed25519 签名）
- **部署文档**：[DEPLOY.md](DEPLOY.md) —— 下载与校验、服务端 / agent 安装（systemd）、配置逐项说明、端口防火墙清单、升级与回滚、公网反代 TLS
- **功能说明**：[FEATURES.md](FEATURES.md) —— 按模块介绍全部功能：监控指标与盘温、告警推送、终端与文件管理、服务集成、追剧、待办文档等

本仓库仅用于发行包分发（同时作为程序内置在线更新源 `github:lyxizq/picopanel-release`），源码不在本仓库。
