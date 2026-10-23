package api

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/engine"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/updmgr"
)

// AgentReportHandler 处理 agent 主动上报（push 模式）：POST /api/v1/agent/report，
// 头 X-Agent-Token 为上报密钥——与服务器一对一（服务端校验唯一），一台 agent
// 一个密钥。与抓取模式共用入库路径：upsert 服务器信息 + 写指标 +
// 告警评估，时间取服务端时钟。
type AgentReportHandler struct {
	st     *store.Store
	eng    *engine.Engine
	updMgr *updmgr.Manager // 396：上报响应捎带升级指令、push 机器回源下载分发束
}

// maxReportBody 单次上报体上限（与指标快照一致，1MB 兜底防滥用）。
const maxReportBody = 1 << 20

// Report 接收一份上报。
func (h *AgentReportHandler) Report(c *gin.Context) {
	token := c.GetHeader("X-Agent-Token")
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少 X-Agent-Token"})
		return
	}

	var r proto.Report
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, maxReportBody)).Decode(&r); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "上报体解析失败"})
		return
	}

	// 身份定位：一对一上报密钥。取出全部 push 密钥做恒时比较——不因命中
	// 提前返回，比较耗时与输入无关（对齐 snapshot 的 subtle 口径）
	toks, err := h.st.PushTokenList()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询服务器失败"})
		return
	}
	id := int64(0)
	for _, t := range toks {
		if subtle.ConstantTimeCompare([]byte(token), []byte(t.Token)) == 1 {
			id = t.ID
		}
	}
	if id == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未知的上报密钥"})
		return
	}

	now := time.Now().Unix() // 存储与告警判定一律用服务端时间，agent 时钟不可信
	if err := h.st.UpsertOnReport(id, r.Sys, now); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新服务器信息失败"})
		return
	}
	if err := h.st.InsertMetric(id, now, &r); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "写入指标失败"})
		return
	}
	h.eng.Evaluate(id, &r, now)
	// 396：push 机器上报带回版本串即确认升级结果（对上目标版本清待领标记）
	if r.Sys != nil && r.Sys.AgentVersion != "" && h.updMgr != nil {
		h.updMgr.ConfirmPushAgent(id, r.Sys.AgentVersion)
	}
	// 组一份响应：PT 中转抓取任务捎带下发（391）+ 在线升级指令（396，按机器
	// 打标后 agent 上报时顺路领走——零额外轮询、零公网暴露）
	resp := gin.H{"code": 0}
	if tasks := h.ptTasksFor(id); len(tasks) > 0 {
		resp["pt_tasks"] = tasks
	}
	if tasks := h.netTasksFor(id); len(tasks) > 0 {
		resp["net_tasks"] = tasks // 通用中转请求任务（548）
	}
	if tasks := h.biliTasksFor(id); len(tasks) > 0 {
		resp["bili_tasks"] = tasks // B 站追更任务（TODO 6；漏挂会导致任务永久 pending、agent 空等）
	}
	if stops := h.biliStopsFor(id); len(stops) > 0 {
		resp["bili_stops"] = stops // 追更手动停止命令（8）
	}
	if tasks := h.videoDlTasksFor(id); len(tasks) > 0 {
		resp["video_dl_tasks"] = tasks // 视频下载任务（TODO 4；漏挂会导致任务永久 pending、agent 空等）
	}
	if stops := h.videoDlStopsFor(id); len(stops) > 0 {
		resp["video_dl_stops"] = stops // 视频下载手动停止命令（TODO 4）
	}
	if tasks := h.appTasksFor(id); len(tasks) > 0 {
		resp["app_tasks"] = tasks // 容器/虚拟机启停操作任务（54；漏挂会永久 pending）
	}
	if h.updMgr != nil {
		if instr := h.updMgr.PushInstruction(id); instr != nil {
			resp["upgrade"] = instr
		}
	}
	c.JSON(http.StatusOK, resp)
}

// ptTasksFor 取该 agent 名下待领 PT 任务（至多 4 个防单轮上报拖太久）。
// 任务里 Cookie 已解密（agent 直接执行用，不出本响应之外）。
func (h *AgentReportHandler) ptTasksFor(serverID int64) []gin.H {
	tasks, err := h.st.ClaimPtTasks(serverID, 4)
	if err != nil || len(tasks) == 0 {
		return nil
	}
	out := make([]gin.H, 0, len(tasks))
	for _, t := range tasks {
		// signin_mode 从站点行现取（签到模式是配置不是凭据，不进任务表快照；
		// dian115 类站 agent 执行时据此决定是否顺带签到）
		mode := ""
		if site, err := h.st.GetPtSite(t.SiteID); err == nil && site != nil {
			mode = site.SigninMode
		}
		out = append(out, gin.H{"task_id": t.ID, "site_id": t.SiteID, "site_key": t.SiteKey,
			"base_url": t.BaseURL, "cookie": t.Cookie, "ua": t.UA, "signin_mode": mode})
	}
	return out
}

// UpgradeBundle push 机器回源下载升级分发束（396）：GET /api/v1/agent/upgrade-bundle，
// X-Agent-Token 鉴权；arch 取查询参数（缺省用该机器上报记录的架构）。
func (h *AgentReportHandler) UpgradeBundle(c *gin.Context) {
	token := c.GetHeader("X-Agent-Token")
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少 X-Agent-Token"})
		return
	}
	toks, err := h.st.PushTokenList()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询服务器失败"})
		return
	}
	id := int64(0)
	for _, t := range toks {
		if subtle.ConstantTimeCompare([]byte(token), []byte(t.Token)) == 1 {
			id = t.ID
		}
	}
	if id == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未知的上报密钥"})
		return
	}
	if row, err := h.st.GetServer(id); err == nil && row != nil {
		h.updMgr.BundleFor(c.Writer, c.Query("arch"), row.Arch)
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "机器不存在"})
}
