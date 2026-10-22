// lz-server 是收集端服务：接收 agent 上报、提供 Web 控制台与告警引擎。
// 手动启动：./lz-server [-config server.yaml]（无配置文件时使用默认值）。
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"lz-monitor/internal/server"
	"lz-monitor/internal/server/config"
	"lz-monitor/internal/update"
)

var version = "dev"

// releasePubKey 由构建注入（deploy/picopanel.pub 的 hex，见 Makefile LDFLAGS），
// 在线更新验签用（395）；empty=构建机缺公钥文件。
var releasePubKey = "empty"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-version", "--version":
			fmt.Println(version)
			return
		case "-check-config", "--check-config":
			// 在线更新的试运行入口：加载配置空载验证（升级器在替换前调用，
			// 「新版本 + 现网配置」组合不过关就中止升级保旧版）
			if _, err := config.LoadConfig(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "配置错误:", err)
				os.Exit(1)
			}
			fmt.Println("配置检查通过")
			return
		}
	}

	cfg, err := config.LoadConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		os.Exit(1)
	}
	// 版本落盘（2026-10-06 用户要求）：可执行文件同目录 VERSION，磁盘可辨版本；
	// 在线升级/手动替换后新进程启动即刷新
	if exe, err := os.Executable(); err == nil {
		if err := update.WriteVersionFile(filepath.Dir(exe), version); err != nil {
			fmt.Fprintln(os.Stderr, "版本文件写入失败（不影响运行）:", err)
		}
	}
	// 升级链路健康自检：Run 内部初始化成功（含监听绑定）会清除标记；启动失败
	// 且标记在位时自动 exec 旧版回滚，绝不留在坏状态（395⑧）
	if err := server.Run(cfg, version, releasePubKey); err != nil {
		marker := update.DefaultMarkerPath()
		if update.MarkerExists(marker) {
			if rbErr := update.Rollback(marker); rbErr != nil {
				fmt.Fprintln(os.Stderr, "自动回滚失败:", rbErr)
			}
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
