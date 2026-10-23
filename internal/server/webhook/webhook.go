// Package webhook 提供通用 Webhook 通知渠道：告警通知与 HTTPS 证书到期提醒
// 共用一份 URL/模板配置（与 WxPusher 并存，各自独立开关）。
//
// 配置存 SQLite settings 表（Web「系统设置」页维护，改后即时生效）：
//
//	webhook_config    JSON（url/template）
//	webhook_on        "1"/"0" 渠道总开关（键缺省视为开，兼容老版本升级用户）
//	alert_webhook_on  "1"/"0" 告警通知开关
//	cert_webhook_on   "1"/"0" 证书到期提醒开关
//
// 推送 = POST 自定义 JSON 模板（占位符 {{title}}/{{message}} 替换为带引号的
// JSON 字符串——模板里占位符直接放值位置、不要再加引号），2xx 视为成功——
// 钉钉/飞书/Slack 等自定义机器人只需按其文档填 URL 与模板即可接入。
package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lz-monitor/internal/server/engine"
	"lz-monitor/internal/server/store"
)

const (
	keyConfig     = "webhook_config"
	keyOn         = "webhook_on"
	keyAlertOn    = "alert_webhook_on"
	keyCertOn     = "cert_webhook_on"
	keyReminderOn = "reminder_wh_on"
	keyLoginOn    = "login_wh_on"
	keyMailOn     = "mail_wh_on"
	keyTvOn       = "tv_wh_on"

	timeout = 10 * time.Second
)

// Config 是一份 Webhook 配置。URL 可能携带密钥（如钉钉 access_token），
// 前端不回显、保存时空值沿用旧值；模板可回显。
type Config struct {
	URL      string `json:"url"`      // 完整回调地址
	Template string `json:"template"` // JSON 模板；空 = 用默认模板
}

// DefaultTemplate URL/模板都为空时的兜底模板。
const DefaultTemplate = `{"title": {{title}}, "content": {{message}}}`

// Ready 配置是否具备推送条件。
func (c Config) Ready() bool { return c.URL != "" }

// LoadConfig 从 settings 表读配置（未配置返回零值）。
func LoadConfig(st *store.Store) (Config, error) {
	var cfg Config
	v, ok, err := st.GetSetting(keyConfig)
	if err != nil || !ok {
		return cfg, err
	}
	err = json.Unmarshal([]byte(v), &cfg)
	return cfg, err
}

// SaveConfig 写回配置。
func SaveConfig(st *store.Store, cfg Config) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return st.SetSetting(keyConfig, string(b))
}

func switchOn(st *store.Store, key string) bool {
	v, ok, err := st.GetSetting(key)
	return err == nil && ok && v == "1"
}

// switchOnDefault 开关缺省视为开（键缺省或为 "1" 都算开，显式 "0" 才算关）。
func switchOnDefault(st *store.Store, key string) bool {
	v, ok, err := st.GetSetting(key)
	if err != nil {
		return false
	}
	return !ok || v == "1"
}

// On 渠道总开关（键缺省视为开，兼容升级前已在用 Webhook 的用户）。
func On(st *store.Store) bool {
	v, ok, err := st.GetSetting(keyOn)
	if err != nil {
		return false
	}
	return !ok || v == "1"
}

// SetOn 写渠道总开关。
func SetOn(st *store.Store, on bool) error { return st.SetSetting(keyOn, boolStr(on)) }

// AlertOn / CertOn 读两个开关（每次推送前现读，开关即时生效）。
func AlertOn(st *store.Store) bool { return switchOn(st, keyAlertOn) }
func CertOn(st *store.Store) bool  { return switchOn(st, keyCertOn) }

// SetAlertOn / SetCertOn 写开关。
func SetAlertOn(st *store.Store, on bool) error {
	return st.SetSetting(keyAlertOn, boolStr(on))
}
func SetCertOn(st *store.Store, on bool) error {
	return st.SetSetting(keyCertOn, boolStr(on))
}

// ReminderOn / SetReminderOn 事项提醒开关（每次推送前现读，即时生效）。
func ReminderOn(st *store.Store) bool { return switchOn(st, keyReminderOn) }
func SetReminderOn(st *store.Store, on bool) error {
	return st.SetSetting(keyReminderOn, boolStr(on))
}

// LoginOn / SetLoginOn 登录提醒开关（缺省视为开：安全通知开箱即用）。
func LoginOn(st *store.Store) bool { return switchOnDefault(st, keyLoginOn) }
func SetLoginOn(st *store.Store, on bool) error {
	return st.SetSetting(keyLoginOn, boolStr(on))
}

// MailOn / SetMailOn 新邮件提醒开关（缺省关：邮件量大易轰炸，用户显式开）。
func MailOn(st *store.Store) bool { return switchOn(st, keyMailOn) }
func SetMailOn(st *store.Store, on bool) error {
	return st.SetSetting(keyMailOn, boolStr(on))
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// jsonStr 把值编码为带引号的 JSON 字符串（占位符替换用，保证模板合法）。
func jsonStr(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// Send 用给定配置推一条通知：subject 作为标题（{{title}}），
// message = subject + 换行 + body（{{message}}）。
func Send(cfg Config, subject, body string) error {
	if !cfg.Ready() {
		return fmt.Errorf("Webhook 配置不完整（缺 URL）")
	}
	tpl := cfg.Template
	if tpl == "" {
		tpl = DefaultTemplate
	}
	payload := tpl
	payload = strings.ReplaceAll(payload, "{{title}}", jsonStr(subject))
	payload = strings.ReplaceAll(payload, "{{message}}", jsonStr(subject+"\n"+body))
	resp, err := (&http.Client{Timeout: timeout}).Post(cfg.URL, "application/json", bytes.NewReader([]byte(payload)))
	if err != nil {
		return fmt.Errorf("请求 Webhook 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	return fmt.Errorf("Webhook 返回 %d: %s", resp.StatusCode, string(snippet))
}

// SendFromStore 按 settings 表当前配置推送（证书提醒等非告警渠道用）。
// 渠道总开关关闭时静默跳过；细分开关由调用方判断，这里只管发。
func SendFromStore(st *store.Store, subject, body string) error {
	if !On(st) {
		return nil
	}
	cfg, err := LoadConfig(st)
	if err != nil {
		return err
	}
	return Send(cfg, subject, body)
}

// AlertNotifier 实现 engine.Notifier：告警触发/恢复时按开关现读配置推送。
type AlertNotifier struct{ st *store.Store }

// NewAlertNotifier 创建告警通知器（装配时注入 engine.New）。
func NewAlertNotifier(st *store.Store) *AlertNotifier { return &AlertNotifier{st: st} }

// Notify 推送一条告警消息；总开关或告警开关未开、配置不全时静默跳过。
func (n *AlertNotifier) Notify(ev engine.AlertEvent) {
	if !On(n.st) || !AlertOn(n.st) {
		return
	}
	name := ev.Alert.ServerName
	if name == "" {
		if srv, err := n.st.GetServer(ev.Alert.ServerID); err == nil {
			name = srv.Name
		} else {
			name = "#" + strconv.FormatInt(int64(ev.Alert.ServerID), 10)
		}
	}
	action := "触发"
	if ev.Action == "resolved" {
		action = "恢复"
	}
	subject := fmt.Sprintf("[PicoPanel] 告警%s：%s", action, name)
	body := fmt.Sprintf("服务器：%s（#%d）\n规则：#%d\n状态：%s\n详情：%s\n时间：%s\n",
		name, ev.Alert.ServerID, ev.Alert.RuleID, action, ev.Alert.Detail,
		time.Unix(ev.Alert.UpdatedAt, 0).Format("2006-01-02 15:04:05"))
	if err := SendFromStore(n.st, subject, body); err != nil {
		log.Printf("告警 Webhook 推送失败: %v", err)
	}
}

// TvOn / SetTvOn 追剧更新开关（548，缺省关：口径与邮件提醒一致，用户显式开）。
func TvOn(st *store.Store) bool { return switchOn(st, keyTvOn) }
func SetTvOn(st *store.Store, on bool) error {
	return st.SetSetting(keyTvOn, boolStr(on))
}
