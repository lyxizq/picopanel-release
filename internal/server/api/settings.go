package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/inspect"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/tvfollow"
	"lz-monitor/internal/server/webhook"
	"lz-monitor/internal/server/webpush"
	"lz-monitor/internal/server/wxpusher"
)

// SettingsHandler 系统参数设置（存 DB settings 表，修改即时生效，无需重启）
// 与手动清理过期数据。
type SettingsHandler struct {
	st            *store.Store
	alertKeepDays int    // 已恢复告警保留天数（仍来自配置文件）
	dbPath        string // 数据库文件路径（展示占用大小用）

	backupMu sync.Mutex // 备份进行中互斥：大库备份数十秒，重复触发直接拒绝（曾出现一次点击产生两份备份）
}

type settingsView struct {
	RetentionDays     int          `json:"retention_days"`      // 指标数据保留天数
	DBSize            int64        `json:"db_size"`             // 数据库文件当前大小（字节）
	WXOn              bool         `json:"wx_on"`               // WxPusher 渠道总开关
	WX                *wxView      `json:"wx,omitempty"`        // WxPusher 配置（未配置过为 null）
	AlertWXOn         bool         `json:"alert_wx_on"`         // 告警 WxPusher 开关
	CertWXOn          bool         `json:"cert_wx_on"`          // 证书到期 WxPusher 开关
	ReminderWXOn      bool         `json:"reminder_wx_on"`      // 事项提醒 WxPusher 开关
	LoginWXOn         bool         `json:"login_wx_on"`         // 登录提醒 WxPusher 开关
	MailWXOn          bool         `json:"mail_wx_on"`          // 新邮件提醒 WxPusher 开关
	WebhookOn         bool         `json:"webhook_on"`          // Webhook 渠道总开关
	Webhook           *webhookView `json:"webhook,omitempty"`   // Webhook 配置（未配置过为 null）
	AlertWebhookOn    bool         `json:"alert_webhook_on"`    // 告警 Webhook 开关
	CertWebhookOn     bool         `json:"cert_webhook_on"`     // 证书到期 Webhook 开关
	ReminderWebhookOn bool         `json:"reminder_wh_on"`      // 事项提醒 Webhook 开关
	LoginWebhookOn    bool         `json:"login_wh_on"`         // 登录提醒 Webhook 开关
	MailWebhookOn     bool         `json:"mail_wh_on"`          // 新邮件提醒 Webhook 开关
	WebPushOn         bool         `json:"webpush_on"`          // Web Push 渠道总开关
	WebPush           webpushView  `json:"webpush"`             // Web Push 状态（公钥/订阅列表）
	AlertWebPushOn    bool         `json:"alert_webpush_on"`    // 告警 Web Push 开关
	CertWebPushOn     bool         `json:"cert_webpush_on"`     // 证书到期 Web Push 开关
	ReminderWebPushOn bool         `json:"reminder_webpush_on"` // 事项提醒 Web Push 开关
	LoginWebPushOn    bool         `json:"login_webpush_on"`    // 登录提醒 Web Push 开关
	MailWebPushOn     bool         `json:"mail_webpush_on"`     // 新邮件提醒 Web Push 开关
	InspectPushOn     bool         `json:"inspect_push_on"`     // 巡检报告每日定时推送开关
	InspectPushTime   string       `json:"inspect_push_time"`   // 定时推送时刻（HH:MM，默认 09:00）
	TvTmdbHasKey      bool         `json:"tv_tmdb_has_key"`     // 追剧 TMDB Key 已配置（key 不回显）
	TvEgressServerID  int64        `json:"tv_egress_server_id"` // 追剧出口 push agent（0=未配置）
	TvWXOn            bool         `json:"tv_wx_on"`            // 追剧 WxPusher 开关
	TvWebhookOn       bool         `json:"tv_wh_on"`            // 追剧 Webhook 开关
	TvWebPushOn       bool         `json:"tv_webpush_on"`       // 追剧 Web Push 开关
}

// webpushView Web Push 渠道状态：公钥（前端 PushManager.subscribe 用）与
// 全部订阅列表（endpoint 含鉴权信息不回显，只给 UA/时间供辨认）。
type webpushView struct {
	PublicKey string           `json:"public_key"` // VAPID 公钥（未生成时空串）
	Subs      []webpushSubView `json:"subs"`
}

type webpushSubView struct {
	ID        int64  `json:"id"`
	UA        string `json:"ua"`
	CreatedAt int64  `json:"created_at"`
	Fp        string `json:"fp"` // endpoint 短指纹（不可逆），前端识别本浏览器自己的订阅用
}

// webhookView Webhook 配置的对外视图：URL 可能携带密钥（如钉钉 access_token）
// 永不回显（空串 + has_url 标记），保存时空 url = 沿用旧值；模板可回显。
type webhookView struct {
	URL      string `json:"url"` // 恒空；HasURL 表示已存有 URL
	HasURL   bool   `json:"has_url"`
	Template string `json:"template"` // JSON 模板（占位符 {{title}}/{{message}}）
}

// wxView WxPusher 配置的对外视图：appToken 永不回显（空串 + has_token 标记），
// 保存时空 token = 沿用旧值。
type wxView struct {
	AppToken string   `json:"app_token"` // 恒空；HasToken 表示已存有 token
	HasToken bool     `json:"has_token"`
	UIDs     []string `json:"uids"`      // 接收者 UID 列表
	TopicIDs []int    `json:"topic_ids"` // 主题 ID 列表（可选）
}

// dbFileSize 数据库主文件当前大小（WAL 定期 checkpoint 并入主文件，主文件即真实占用）。
func (h *SettingsHandler) dbFileSize() int64 {
	if h.dbPath == "" {
		return 0
	}
	fi, err := os.Stat(h.dbPath)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// Get 返回当前参数。
func (h *SettingsHandler) Get(c *gin.Context) {
	days, err := h.st.RetentionDays()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询参数失败"})
		return
	}
	view := settingsView{
		RetentionDays: days, DBSize: h.dbFileSize(),
		WXOn:      wxpusher.On(h.st),
		AlertWXOn: wxpusher.AlertWXOn(h.st), CertWXOn: wxpusher.CertWXOn(h.st),
		ReminderWXOn:   wxpusher.ReminderWXOn(h.st),
		LoginWXOn:      wxpusher.LoginWXOn(h.st),
		MailWXOn:       wxpusher.MailWXOn(h.st),
		WebhookOn:      webhook.On(h.st),
		AlertWebhookOn: webhook.AlertOn(h.st), CertWebhookOn: webhook.CertOn(h.st),
		ReminderWebhookOn: webhook.ReminderOn(h.st),
		LoginWebhookOn:    webhook.LoginOn(h.st),
		MailWebhookOn:     webhook.MailOn(h.st),
		WebPushOn:         webpush.On(h.st),
		AlertWebPushOn:    webpush.AlertOn(h.st), CertWebPushOn: webpush.CertOn(h.st),
		ReminderWebPushOn: webpush.ReminderOn(h.st),
		LoginWebPushOn:    webpush.LoginOn(h.st),
		MailWebPushOn:     webpush.MailOn(h.st),
		InspectPushOn:     inspect.PushOn(h.st), InspectPushTime: inspect.PushTime(h.st),
		TvTmdbHasKey:     tvfollow.TmdbKey(h.st) != "",
		TvEgressServerID: tvfollow.EgressServerID(h.st),
		TvWXOn:           wxpusher.TvWXOn(h.st),
		TvWebhookOn:      webhook.TvOn(h.st),
		TvWebPushOn:      webpush.TvOn(h.st),
	}
	if cfg, err := wxpusher.LoadConfig(h.st); err == nil && cfg.AppToken != "" {
		view.WX = &wxView{
			HasToken: true, UIDs: cfg.UIDs, TopicIDs: cfg.TopicIDs,
		}
	}
	if cfg, err := webhook.LoadConfig(h.st); err == nil && cfg.URL != "" {
		view.Webhook = &webhookView{HasURL: true, Template: cfg.Template}
	}
	if pk, err := webpush.VAPIDPublicKey(h.st); err == nil {
		view.WebPush.PublicKey = pk
	}
	if subs, err := h.st.ListPushSubs(); err == nil {
		view.WebPush.Subs = make([]webpushSubView, 0, len(subs))
		for _, s := range subs {
			view.WebPush.Subs = append(view.WebPush.Subs, webpushSubView{
				ID: s.ID, UA: s.UA, CreatedAt: s.CreatedAt, Fp: webpush.Fingerprint(s.Endpoint),
			})
		}
	}
	c.JSON(http.StatusOK, view)
}

type updateSettingsReq struct {
	RetentionDays     *int         `json:"retention_days"` // 缺省/0 = 不修改
	WXOn              *bool        `json:"wx_on"`          // 渠道总开关：关 = 配置与推送全部停用
	WX                *wxView      `json:"wx"`             // 缺省 = 不修改；app_token 空 = 沿用旧值
	AlertWXOn         *bool        `json:"alert_wx_on"`
	CertWXOn          *bool        `json:"cert_wx_on"`
	ReminderWXOn      *bool        `json:"reminder_wx_on"`
	LoginWXOn         *bool        `json:"login_wx_on"`
	MailWXOn          *bool        `json:"mail_wx_on"`
	WebhookOn         *bool        `json:"webhook_on"`
	Webhook           *webhookView `json:"webhook"` // 缺省 = 不修改；url 空 = 沿用旧值
	AlertWebhookOn    *bool        `json:"alert_webhook_on"`
	CertWebhookOn     *bool        `json:"cert_webhook_on"`
	ReminderWebhookOn *bool        `json:"reminder_wh_on"`
	LoginWebhookOn    *bool        `json:"login_wh_on"`
	MailWebhookOn     *bool        `json:"mail_wh_on"`
	WebPushOn         *bool        `json:"webpush_on"` // 开启时自动生成 VAPID 密钥
	AlertWebPushOn    *bool        `json:"alert_webpush_on"`
	CertWebPushOn     *bool        `json:"cert_webpush_on"`
	ReminderWebPushOn *bool        `json:"reminder_webpush_on"`
	LoginWebPushOn    *bool        `json:"login_webpush_on"`
	MailWebPushOn     *bool        `json:"mail_webpush_on"`
	InspectPushOn     *bool        `json:"inspect_push_on"` // 巡检报告定时推送
	InspectPushTime   *string      `json:"inspect_push_time"`
	TvTmdbKey         *string      `json:"tv_tmdb_key"`         // 空 = 沿用旧值
	TvEgressServerID  *int64       `json:"tv_egress_server_id"` // 追剧出口 push agent
	TvWXOn            *bool        `json:"tv_wx_on"`
	TvWebhookOn       *bool        `json:"tv_wh_on"`
	TvWebPushOn       *bool        `json:"tv_webpush_on"`
}

// Put 修改参数（保留天数 / 邮件配置 / 邮件开关），改后即时生效。
func (h *SettingsHandler) Put(c *gin.Context) {
	var req updateSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误"})
		return
	}
	if req.RetentionDays != nil {
		if *req.RetentionDays < 1 || *req.RetentionDays > 3650 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "数据保存天数应为 1~3650 天"})
			return
		}
		if err := h.st.SetRetentionDays(*req.RetentionDays); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存保留天数失败"})
			return
		}
	}
	if req.WX != nil {
		if !wxpusher.On(h.st) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "WxPusher 渠道总开关未开启，请先开启后再配置"})
			return
		}
		// app_token 空 = 沿用已存 token（前端不回显，编辑其他字段不必重输）
		old, _ := wxpusher.LoadConfig(h.st)
		cfg := wxpusher.Config{UIDs: req.WX.UIDs, TopicIDs: req.WX.TopicIDs}
		if req.WX.AppToken != "" {
			cfg.AppToken = req.WX.AppToken
		} else {
			cfg.AppToken = old.AppToken
		}
		if err := wxpusher.SaveConfig(h.st, cfg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存 WxPusher 配置失败"})
			return
		}
	}
	if req.AlertWXOn != nil {
		if err := wxpusher.SetAlertWXOn(h.st, *req.AlertWXOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存告警通知开关失败"})
			return
		}
	}
	if req.CertWXOn != nil {
		if err := wxpusher.SetCertWXOn(h.st, *req.CertWXOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存证书提醒开关失败"})
			return
		}
	}
	if req.ReminderWXOn != nil {
		if err := wxpusher.SetReminderWXOn(h.st, *req.ReminderWXOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存事项提醒开关失败"})
			return
		}
	}
	if req.LoginWXOn != nil {
		if err := wxpusher.SetLoginWXOn(h.st, *req.LoginWXOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存登录提醒开关失败"})
			return
		}
	}
	if req.MailWXOn != nil {
		if err := wxpusher.SetMailWXOn(h.st, *req.MailWXOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存新邮件提醒开关失败"})
			return
		}
	}
	if req.Webhook != nil {
		if !webhook.On(h.st) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Webhook 渠道总开关未开启，请先开启后再配置"})
			return
		}
		// url 空 = 沿用已存 URL（URL 常携带密钥，前端不回显，编辑模板不必重输）
		old, _ := webhook.LoadConfig(h.st)
		cfg := webhook.Config{Template: req.Webhook.Template}
		if req.Webhook.URL != "" {
			cfg.URL = req.Webhook.URL
		} else {
			cfg.URL = old.URL
		}
		if err := webhook.SaveConfig(h.st, cfg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存 Webhook 配置失败"})
			return
		}
	}
	if req.AlertWebhookOn != nil {
		if err := webhook.SetAlertOn(h.st, *req.AlertWebhookOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存告警通知开关失败"})
			return
		}
	}
	if req.CertWebhookOn != nil {
		if err := webhook.SetCertOn(h.st, *req.CertWebhookOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存证书提醒开关失败"})
			return
		}
	}
	if req.ReminderWebhookOn != nil {
		if err := webhook.SetReminderOn(h.st, *req.ReminderWebhookOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存事项提醒开关失败"})
			return
		}
	}
	if req.LoginWebhookOn != nil {
		if err := webhook.SetLoginOn(h.st, *req.LoginWebhookOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存登录提醒开关失败"})
			return
		}
	}
	if req.MailWebhookOn != nil {
		if err := webhook.SetMailOn(h.st, *req.MailWebhookOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存新邮件提醒开关失败"})
			return
		}
	}
	if req.WXOn != nil {
		if err := wxpusher.SetOn(h.st, *req.WXOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存 WxPusher 总开关失败"})
			return
		}
	}
	if req.WebhookOn != nil {
		if err := webhook.SetOn(h.st, *req.WebhookOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存 Webhook 总开关失败"})
			return
		}
	}
	if req.WebPushOn != nil {
		if err := webpush.SetOn(h.st, *req.WebPushOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存 Web Push 总开关失败: " + err.Error()})
			return
		}
	}
	if req.AlertWebPushOn != nil {
		if err := webpush.SetAlertOn(h.st, *req.AlertWebPushOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存告警通知开关失败"})
			return
		}
	}
	if req.CertWebPushOn != nil {
		if err := webpush.SetCertOn(h.st, *req.CertWebPushOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存证书提醒开关失败"})
			return
		}
	}
	if req.ReminderWebPushOn != nil {
		if err := webpush.SetReminderOn(h.st, *req.ReminderWebPushOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存事项提醒开关失败"})
			return
		}
	}
	if req.LoginWebPushOn != nil {
		if err := webpush.SetLoginOn(h.st, *req.LoginWebPushOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存登录提醒开关失败"})
			return
		}
	}
	if req.MailWebPushOn != nil {
		if err := webpush.SetMailOn(h.st, *req.MailWebPushOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存新邮件提醒开关失败"})
			return
		}
	}
	if req.InspectPushOn != nil || req.InspectPushTime != nil {
		on := inspect.PushOn(h.st)
		t := ""
		if req.InspectPushOn != nil {
			on = *req.InspectPushOn
		}
		if req.InspectPushTime != nil {
			t = strings.TrimSpace(*req.InspectPushTime)
		}
		if err := inspect.SetPushConfig(h.st, on, t); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if req.TvTmdbKey != nil && strings.TrimSpace(*req.TvTmdbKey) != "" {
		if err := tvfollow.SetTmdbKey(h.st, strings.TrimSpace(*req.TvTmdbKey)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存 TMDB Key 失败"})
			return
		}
	}
	if req.TvEgressServerID != nil {
		old := tvfollow.EgressServerID(h.st)
		if err := tvfollow.SetEgressServerID(h.st, *req.TvEgressServerID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存追剧出口失败"})
			return
		}
		if old != *req.TvEgressServerID {
			// 换绑出口：作废旧出口名下 pending 任务，条目本窗口内立即重查
			h.st.ExpireNetTasksByServer(old)
			h.st.ResetTVFollowQueue()
		}
	}
	if req.TvWXOn != nil {
		if err := wxpusher.SetTvWXOn(h.st, *req.TvWXOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存追剧推送开关失败"})
			return
		}
	}
	if req.TvWebhookOn != nil {
		if err := webhook.SetTvOn(h.st, *req.TvWebhookOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存追剧推送开关失败"})
			return
		}
	}
	if req.TvWebPushOn != nil {
		if err := webpush.SetTvOn(h.st, *req.TvWebPushOn); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存追剧推送开关失败"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// WXTest 用当前已存配置发一条测试消息（保存后先测试再启用开关）。
func (h *SettingsHandler) WXTest(c *gin.Context) {
	if !wxpusher.On(h.st) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "WxPusher 渠道总开关未开启"})
		return
	}
	cfg, err := wxpusher.LoadConfig(h.st)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取 WxPusher 配置失败"})
		return
	}
	if !cfg.Ready() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先保存完整的 WxPusher 配置（appToken 与接收者）"})
		return
	}
	if err := wxpusher.Send(cfg, "[PicoPanel] 测试消息", "这是一条测试消息，收到即说明 WxPusher 通知配置正确。"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// WebhookTest 用当前已存配置发一条测试消息（保存后先测试再启用开关）。
func (h *SettingsHandler) WebhookTest(c *gin.Context) {
	if !webhook.On(h.st) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Webhook 渠道总开关未开启"})
		return
	}
	cfg, err := webhook.LoadConfig(h.st)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取 Webhook 配置失败"})
		return
	}
	if !cfg.Ready() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先保存 Webhook 配置（URL）"})
		return
	}
	if err := webhook.Send(cfg, "[PicoPanel] 测试消息", "这是一条测试消息，收到即说明 Webhook 通知配置正确。"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// WebPushTest 向全部浏览器订阅发一条测试通知（总开关关或无订阅时报错提示）。
func (h *SettingsHandler) WebPushTest(c *gin.Context) {
	if !webpush.On(h.st) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Web Push 渠道总开关未开启"})
		return
	}
	if n, err := h.st.CountPushSubs(); err == nil && n == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "还没有浏览器订阅，请先在本页点击「订阅此浏览器」"})
		return
	}
	if err := webpush.SendFromStore(h.st, "[PicoPanel] 测试通知", "这是一条测试通知，收到即说明 Web Push 配置正确。"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Cleanup 立即清理一次过期数据：保留期外的指标 + 已恢复告警 + incremental_vacuum
// 回收磁盘空间（与后台每小时自动清理同一套逻辑）。
func (h *SettingsHandler) Cleanup(c *gin.Context) {
	days, err := h.st.RetentionDays()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询数据保留天数失败"})
		return
	}
	now := time.Now().Unix()
	nm, err := h.st.DeleteMetricsBefore(now-int64(days)*86400, 5000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "清理过期指标失败"})
		return
	}
	na, err := h.st.DeleteResolvedBefore(now - int64(h.alertKeepDays)*86400)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "清理过期告警失败"})
		return
	}
	if err := h.st.Vacuum(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "回收数据库空间失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"retention_days":  days,
		"metrics_deleted": nm,
		"alerts_deleted":  na,
		"db_size":         h.dbFileSize(), // 清理回收后的新大小
	})
}

// DBBackup 创建数据库备份：VACUUM INTO 一致性快照（含 WAL 内已提交数据，
// 内容与线上一致）落到数据库同目录 backup/ 下，gzip 最高压缩后只保留
// .gz——文件名为「数据库文件名-时间戳.gz」，管理员可经 SFTP/scp 手动取回，
// 解压即得 standalone 完整库，可直接用于恢复或重新部署。
func (h *SettingsHandler) DBBackup(c *gin.Context) {
	if !h.backupMu.TryLock() {
		c.JSON(http.StatusConflict, gin.H{"error": "已有备份正在进行中，请等它完成后再试"})
		return
	}
	defer h.backupMu.Unlock()
	if h.dbPath == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "未配置数据库路径"})
		return
	}
	dir := filepath.Join(filepath.Dir(h.dbPath), "backup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建备份目录失败: " + err.Error()})
		return
	}
	base := filepath.Base(h.dbPath)
	stem := base + "-" + time.Now().Format("20060102-150405")
	// 中间快照与最终 .gz 同目录，保证同一文件系统、rename 语义简单
	tmp := filepath.Join(dir, stem+".tmp")
	final := filepath.Join(dir, stem+".gz")
	defer os.Remove(tmp) // 成功与否都清理中间快照（成功时已不存在，无害）
	if err := h.st.BackupTo(tmp); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成快照失败: " + err.Error()})
		return
	}
	src, err := os.Open(tmp)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取快照失败: " + err.Error()})
		return
	}
	dst, err := os.Create(final)
	if err != nil {
		src.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建压缩文件失败: " + err.Error()})
		return
	}
	gz, err := gzip.NewWriterLevel(dst, gzip.BestCompression)
	if err != nil {
		dst.Close()
		src.Close()
		os.Remove(final)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "压缩快照失败: " + err.Error()})
		return
	}
	if _, err = io.Copy(gz, src); err == nil {
		err = gz.Close()
	}
	if err == nil {
		err = dst.Close()
	} else {
		dst.Close()
	}
	src.Close()
	if err != nil {
		os.Remove(final) // 半截压缩文件不留着冒充备份
		c.JSON(http.StatusInternalServerError, gin.H{"error": "压缩快照失败: " + err.Error()})
		return
	}
	fi, _ := os.Stat(final)
	size := int64(0)
	if fi != nil {
		size = fi.Size()
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "file": final, "size": size})
}
