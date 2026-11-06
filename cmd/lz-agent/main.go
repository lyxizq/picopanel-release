// lz-agent 是部署在被监控 Linux 服务器上的采集程序。
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"lz-monitor/internal/agent"
	"lz-monitor/internal/update"
)

var version = "dev"

// releasePubKey 由构建注入（deploy/picopanel.pub 的 hex，见 Makefile LDFLAGS），
// 在线更新验签用（396）；empty=构建机缺公钥文件。
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
			if _, err := agent.LoadConfig(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "配置错误:", err)
				os.Exit(1)
			}
			fmt.Println("配置检查通过")
			return
		}
	}

	cfg, err := agent.LoadConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		os.Exit(1)
	}
	// 版本落盘（2026-10-06 用户要求）：可执行文件同目录 VERSION，磁盘可辨版本；
	// agent「零磁盘写入」原则的例外仅此一处（换版本才写、静态小文件）
	if exe, err := os.Executable(); err == nil {
		if err := update.WriteVersionFile(filepath.Dir(exe), version); err != nil {
			fmt.Fprintln(os.Stderr, "版本文件写入失败（不影响运行）:", err)
		}
	}
	// 升级链路健康自检：Run 内部初始化成功会清除标记；走到这里的错误若发生在
	// 升级后（标记在位），自动 exec 旧版回滚，不留在坏状态（395⑧）
	if err := agent.Run(cfg, agent.Runtime{Version: version, PubKey: releasePubKey}); err != nil {
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
