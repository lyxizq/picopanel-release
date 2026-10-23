// Package wxpusher 提供 WXPusher 微信推送通知：告警通知与 HTTPS 证书到期提醒共用。
//
// 配置存 SQLite settings 表（Web「系统设置」页维护，改后即时生效，无需重启）：
//
//	wxpusher_config JSON（app_token/uids/topic_ids）
//	wxpush_on       "1"/"0" 渠道总开关（键缺省视为开，兼容老版本升级用户）
//	alert_wx_on     "1"/"0" 告警通知开关
//	cert_wx_on      "1"/"0" 证书到期提醒开关
//
// WXPusher（https://wxpusher.zjiecode.com）无需自建服务：注册应用拿 appToken，
// 用户扫码关注拿 UID（或建主题拿 topicId），POST 一段 JSON 即推送到微信。
package wxpusher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"lz-monitor/internal/server/engine"
	"lz-monitor/internal/server/store"
)

const (
	keyWXConfig   = "wxpusher_config"
	keyOn         = "wxpush_on"
	keyAlertOn    = "alert_wx_on"
	keyCertOn     = "cert_wx_on"
	keyReminderOn = "reminder_wx_on"
	keyLoginOn    = "login_wx_on"
	keyMailOn     = "mail_wx_on"
	keyTvOn       = "tv_wx_on"

	apiURL  = "https://wxpusher.zjiecode.com/api/send/message"
	timeout = 10 * time.Second
)

// Config 是一份 WxPusher 配置。UID 与主题至少配一种；两者都配时同一消息
// 会发给「UID 用户 ∪ 主题订阅者」（去重由 WxPusher 侧处理）。
type Config struct {
	AppToken string   `json:"app_token"` // 应用 appToken（AT_ 开头）
	UIDs     []string `json:"uids"`      // 接收者 UID 列表（UID_ 开头）
	TopicIDs []int    `json:"topic_ids"` // 主题 ID 列表（可选）
}

// Ready 配置是否具备推送条件（appToken + 至少一个接收目标）。
func (c Config) Ready() bool {
	return c.AppToken != "" && (len(c.UIDs) > 0 || len(c.TopicIDs) > 0)
}

// LoadConfig 从 settings 表读配置（未配置返回零值）。
func LoadConfig(st *store.Store) (Config, error) {
	var cfg Config
	v, ok, err := st.GetSetting(keyWXConfig)
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
	return st.SetSetting(keyWXConfig, string(b))
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

// On 渠道总开关（键缺省视为开，兼容升级前已在用 WxPusher 的用户）。
func On(st *store.Store) bool {
	v, ok, err := st.GetSetting(keyOn)
	if err != nil {
		return false
	}
	return !ok || v == "1"
}

// SetOn 写渠道总开关。
func SetOn(st *store.Store, on bool) error { return st.SetSetting(keyOn, boolStr(on)) }

// AlertWXOn / CertWXOn 读两个开关（每次推送前现读，开关即时生效）。
func AlertWXOn(st *store.Store) bool { return switchOn(st, keyAlertOn) }
func CertWXOn(st *store.Store) bool  { return switchOn(st, keyCertOn) }

// SetAlertWXOn / SetCertWXOn 写开关。
func SetAlertWXOn(st *store.Store, on bool) error {
	return st.SetSetting(keyAlertOn, boolStr(on))
}
func SetCertWXOn(st *store.Store, on bool) error {
	return st.SetSetting(keyCertOn, boolStr(on))
}

// ReminderWXOn / SetReminderWXOn 事项提醒开关（每次推送前现读，即时生效）。
func ReminderWXOn(st *store.Store) bool { return switchOn(st, keyReminderOn) }
func SetReminderWXOn(st *store.Store, on bool) error {
	return st.SetSetting(keyReminderOn, boolStr(on))
}

// LoginWXOn / SetLoginWXOn 登录提醒开关（缺省视为开：登录提醒是安全通知，
// 开箱即用；显式关过才停发）。
func LoginWXOn(st *store.Store) bool { return switchOnDefault(st, keyLoginOn) }
func SetLoginWXOn(st *store.Store, on bool) error {
	return st.SetSetting(keyLoginOn, boolStr(on))
}

// MailWXOn / SetMailWXOn 新邮件提醒开关（缺省关：邮件量大易轰炸，用户显式开）。
func MailWXOn(st *store.Store) bool { return switchOn(st, keyMailOn) }
func SetMailWXOn(st *store.Store, on bool) error {
	return st.SetSetting(keyMailOn, boolStr(on))
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// wxResp 是 WxPusher 推送接口的应答；code=1000 表示成功。
type wxResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// Send 用给定配置推一条纯文本消息（subject 作为首行与摘要）。
func Send(cfg Config, subject, body string) error {
	if !cfg.Ready() {
		return fmt.Errorf("WxPusher 配置不完整（缺 appToken 或接收者）")
	}
	content := subject + "\n" + body
	summary := subject
	if r := []rune(summary); len(r) > 99 {
		summary = string(r[:99]) + "…"
	}
	payload := map[string]any{
		"appToken":    cfg.AppToken,
		"content":     content,
		"summary":     summary,
		"contentType": 1,
		"uids":        cfg.UIDs,
		"topicIds":    cfg.TopicIDs,
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	cl := &http.Client{Timeout: timeout}
	resp, err := cl.Post(apiURL, "application/json", bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("请求 WxPusher 失败: %w", err)
	}
	defer resp.Body.Close()
	var r wxResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("WxPusher 应答解析失败: %w", err)
	}
	if r.Code != 1000 {
		return fmt.Errorf("WxPusher 推送失败: code=%d %s", r.Code, r.Msg)
	}
	return nil
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
// 每次现读 settings——开关与服务器改动的即时生效，代价是一次索引点查。
type AlertNotifier struct{ st *store.Store }

// NewAlertNotifier 创建告警通知器（装配时注入 engine.New）。
func NewAlertNotifier(st *store.Store) *AlertNotifier { return &AlertNotifier{st: st} }

// Notify 推送一条告警消息；总开关或告警开关未开、配置不全时静默跳过。
func (n *AlertNotifier) Notify(ev engine.AlertEvent) {
	if !On(n.st) || !AlertWXOn(n.st) {
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
		// 通知失败只打日志不重试，告警本身已落库不受影响
		log.Printf("告警 WxPusher 推送失败: %v", err)
	}
}

// TvWXOn / SetTvWXOn 追剧更新开关（548，缺省关：口径与邮件提醒一致，用户显式开）。
func TvWXOn(st *store.Store) bool { return switchOn(st, keyTvOn) }
func SetTvWXOn(st *store.Store, on bool) error {
	return st.SetSetting(keyTvOn, boolStr(on))
}
