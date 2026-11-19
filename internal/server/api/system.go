package api

import (
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/gin-gonic/gin"
)

// SystemHandler 服务端自身控制：经部署脚本 lz-server.sh 重启 / 更新重启。
// 脚本与可执行文件同目录（release/deploy/server/ 布局保证）。
type SystemHandler struct{}

type restartReq struct {
	Mode string `json:"mode" binding:"required"` // restart | upgrade
}

// serverScriptPath 定位部署脚本（可执行文件同目录 lz-server.sh）。
func serverScriptPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "lz-server.sh"), nil
}

// triggerRestart 调度脚本重启：独立会话 + 后台，当前进程退出不影响脚本继续
// 执行（Setsid 脱离进程组）；sleep 2 让响应先送达浏览器。
func triggerRestart(script, mode string) error {
	cmd := exec.Command("sh", "-c", "sleep 2 && exec \""+script+"\" "+mode)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// Restart 触发服务端重启。响应先返回（sleep 2 让响应送达），再由独立会话的
// 脚本执行。upgrade = 脚本把同目录分发包 lz-server.gz 解压替换后重启
// （make release 的升级流程）；.gz 不在位时拒绝，避免停机后起不来。
func (h *SystemHandler) Restart(c *gin.Context) {
	var req restartReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if req.Mode != "restart" && req.Mode != "upgrade" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mode 应为 restart 或 upgrade"})
		return
	}
	script, err := serverScriptPath()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "定位服务端程序失败"})
		return
	}
	if _, err := os.Stat(script); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未找到部署脚本 lz-server.sh（仅支持脚本部署方式）"})
		return
	}
	// 脚本无执行权限时 sh 会在 exec 处静默失败（Start 仍成功、响应显示成功，
	// 实际什么都没发生）——这里提前拦下并给出修复命令
	if info, err := os.Stat(script); err == nil && info.Mode()&0o100 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "部署脚本无执行权限：请在服务器执行 chmod +x " + script + " 后重试"})
		return
	}
	if req.Mode == "upgrade" {
		if _, err := os.Stat(filepath.Join(filepath.Dir(script), "lz-server.gz")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "同目录下没有 lz-server.gz，请先上传新版本压缩包再更新重启"})
			return
		}
	}
	if err := triggerRestart(script, req.Mode); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "启动重启脚本失败: " + err.Error()})
		return
	}
	log.Printf("已触发网页%s（%s %s）",
		map[string]string{"restart": "重启", "upgrade": "更新重启"}[req.Mode], script, req.Mode)
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "服务端将在约 2 秒后" +
		map[string]string{"restart": "重启", "upgrade": "更新并重启"}[req.Mode] + "，页面请稍后重新访问"})
}
