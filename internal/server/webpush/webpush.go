// Package webpush 提供 Web Push 浏览器推送渠道（Push API + VAPID 鉴权，
// 消息加密走 RFC 8291，经 webpush-go 实现）：告警触发/恢复、证书临期、
// 巡检报告、提醒事项均按开关经此推送，与 WxPusher/Webhook 并列。
//
// settings 表键：
//
//	webpush_vapid     JSON {public_key, private_key}（首次开启时自动生成并固定，
//	                  密钥不变则浏览器订阅长期有效；换密钥所有订阅需重新订阅）
//	webpush_on        "1"/"0" 渠道总开关（缺省关：需显式开启后才可配置与推送）
//	alert_webpush_on  "1"/"0" 告警通知细分开关
//	cert_webpush_on   "1"/"0" 证书到期提醒细分开关
//
// 订阅存 push_subscriptions 表（endpoint 唯一），推送应答 404/410（订阅过期）
// 时自动删除该订阅。浏览器侧需 HTTPS（或 localhost）安全上下文才能订阅。
package webpush

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SherClockHolmes/webpush-go"

	"lz-monitor/internal/server/engine"
	"lz-monitor/internal/server/store"
)

const (
	keyVAPID      = "webpush_vapid"
	keyOn         = "webpush_on"
	keyAlertOn    = "alert_webpush_on"
	keyCertOn     = "cert_webpush_on"
	keyReminderOn = "reminder_webpush_on"
	keyLoginOn    = "login_webpush_on"
	keyMailOn     = "mail_webpush_on"
	keyTvOn       = "tv_webpush_on"
	keyAssistOn   = "assist_webpush_on"
	// VAPID JWT 的 sub 联系人字段：Apple 推送服务（web.push.apple.com，iOS 订阅
	// 都走它）强制要求 mailto:/https:// URI，不合法直接 403（Google FCM 不校验，
	// 故桌面端此前一直正常）。注意 webpush-go 对非 https 开头的值自动补 "mailto:"
	// 前缀（vapid.go），所以这里必须给裸邮箱，填带 mailto: 的值会变成双重前缀。
	subscriber     = "admin@example.com"
	sendTimeout    = 10 * time.Second
	httpGoneStatus = 410
)

// vapidKeys 是存 settings 的 VAPID 密钥对（base64url）。
type vapidKeys struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

func switchOn(st *store.Store, key string) bool {
	v, ok, err := st.GetSetting(key)
	return err == nil && ok && v == "1"
}

func setSwitch(st *store.Store, key string, on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return st.SetSetting(key, v)
}

// On 渠道总开关（缺省关）。
func On(st *store.Store) bool { return switchOn(st, keyOn) }

// SetOn 写总开关；开启时自动生成 VAPID 密钥（已存在则不动）。
func SetOn(st *store.Store, on bool) error {
	if err := setSwitch(st, keyOn, on); err != nil {
		return err
	}
	if on {
		if _, err := EnsureVAPID(st); err != nil {
			return err
		}
	}
	return nil
}

// AlertOn / CertOn 读两个细分开关。
func AlertOn(st *store.Store) bool { return switchOn(st, keyAlertOn) }
func CertOn(st *store.Store) bool  { return switchOn(st, keyCertOn) }

// SetAlertOn / SetCertOn 写细分开关。
func SetAlertOn(st *store.Store, on bool) error { return setSwitch(st, keyAlertOn, on) }
func SetCertOn(st *store.Store, on bool) error  { return setSwitch(st, keyCertOn, on) }

// ReminderOn / SetReminderOn 事项提醒细分开关。
func ReminderOn(st *store.Store) bool { return switchOn(st, keyReminderOn) }
func SetReminderOn(st *store.Store, on bool) error {
	return setSwitch(st, keyReminderOn, on)
}

// MailOn / SetMailOn 新邮件提醒细分开关（缺省关：邮件量大易轰炸，用户显式开；
// 渠道总开关仍默认关，两关全开才推送）。
func MailOn(st *store.Store) bool { return switchOn(st, keyMailOn) }
func SetMailOn(st *store.Store, on bool) error {
	return setSwitch(st, keyMailOn, on)
}

// LoginOn / SetLoginOn 登录提醒细分开关（缺省视为开：安全通知开箱即用；
// 渠道总开关仍默认关，两关全开才推送）。
func LoginOn(st *store.Store) bool { return switchOnDefault(st, keyLoginOn) }
func SetLoginOn(st *store.Store, on bool) error {
	return setSwitch(st, keyLoginOn, on)
}

// switchOnDefault 开关缺省视为开（键缺省或为 "1" 都算开，显式 "0" 才算关）。
func switchOnDefault(st *store.Store, key string) bool {
	v, ok, err := st.GetSetting(key)
	if err != nil {
		return false
	}
	return !ok || v == "1"
}

// EnsureVAPID 读取 VAPID 密钥，没有则生成并落库（幂等）。
func EnsureVAPID(st *store.Store) (vapidKeys, error) {
	var k vapidKeys
	if v, ok, err := st.GetSetting(keyVAPID); err == nil && ok {
		if err := json.Unmarshal([]byte(v), &k); err == nil && k.PublicKey != "" && k.PrivateKey != "" {
			return k, nil
		}
	}
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return k, fmt.Errorf("生成 VAPID 密钥失败: %w", err)
	}
	k = vapidKeys{PublicKey: pub, PrivateKey: priv}
	b, err := json.Marshal(k)
	if err != nil {
		return k, err
	}
	if err := st.SetSetting(keyVAPID, string(b)); err != nil {
		return k, err
	}
	return k, nil
}

// VAPIDPublicKey 返回公钥（无则生成），前端 PushManager.subscribe 用。
func VAPIDPublicKey(st *store.Store) (string, error) {
	k, err := EnsureVAPID(st)
	if err != nil {
		return "", err
	}
	return k.PublicKey, nil
}

// SaveSubscription 落一条浏览器订阅。
func SaveSubscription(st *store.Store, endpoint, p256dh, auth, ua string) error {
	return st.UpsertPushSub(&store.PushSub{
		Endpoint: endpoint, P256DH: p256dh, Auth: auth, UA: ua, CreatedAt: time.Now().Unix(),
	})
}

// RemoveSubscription 退订。
func RemoveSubscription(st *store.Store, endpoint string) error {
	return st.DeletePushSub(endpoint)
}

// RemoveSubscriptionByID 按行 ID 删除（设置页订阅列表删除按钮）。
func RemoveSubscriptionByID(st *store.Store, id int64) error {
	return st.DeletePushSubByID(id)
}

// Fingerprint endpoint 的短指纹（SHA-256 前 16 个 hex 字符）：设置页订阅列表
// 不回显 endpoint（含鉴权信息），前端用它判断某行是否本浏览器自己的订阅，
// 删除自己的订阅时同步清理本地 pushManager 状态。不可逆，不泄露 endpoint。
func Fingerprint(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:])[:16]
}

// Send 向全部订阅推送一条消息（title/正文）。总开关关闭时静默跳过；
// 单个订阅失败只记日志，404/410 顺手清理；全部失败才返回错误。
func Send(st *store.Store, subject, body string) error {
	if !On(st) {
		return nil
	}
	return send(st, subject, body)
}

func send(st *store.Store, subject, body string) error {
	k, err := EnsureVAPID(st)
	if err != nil {
		return err
	}
	subs, err := st.ListPushSubs()
	if err != nil {
		return err
	}
	if len(subs) == 0 {
		return fmt.Errorf("还没有浏览器订阅，请先在消息推送页订阅")
	}
	payload, _ := json.Marshal(map[string]string{"title": subject, "body": body})
	cl := &http.Client{Timeout: sendTimeout}
	gone := 0
	failed := 0
	for i := range subs {
		sub := &subs[i]
		resp, err := webpush.SendNotification(payload, &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys:     webpush.Keys{P256dh: sub.P256DH, Auth: sub.Auth},
		}, &webpush.Options{
			Subscriber:      subscriber,
			TTL:             12 * 3600,
			VAPIDPublicKey:  k.PublicKey,
			VAPIDPrivateKey: k.PrivateKey,
			HTTPClient:      cl,
		})
		if err != nil {
			failed++
			log.Printf("Web Push 推送失败 (sub=%d): %v", sub.ID, err)
			continue
		}
		resp.Body.Close()
		// 404/410：订阅已失效（浏览器退订/过期），从库里清掉
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == httpGoneStatus {
			gone++
			if err := st.DeletePushSub(sub.Endpoint); err != nil {
				log.Printf("清理失效订阅失败 (sub=%d): %v", sub.ID, err)
			}
		} else if resp.StatusCode >= 300 {
			failed++
			// 响应体带推送服务的拒绝原因（如 Apple 的 {"reason":...}），截 512 字节进日志便于排查
			reason, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			log.Printf("Web Push 推送应答异常 (sub=%d): %d %s", sub.ID, resp.StatusCode, strings.TrimSpace(string(reason)))
		}
	}
	if gone > 0 {
		log.Printf("Web Push：已清理 %d 条失效订阅", gone)
	}
	if failed > 0 && failed+gone >= len(subs) {
		return fmt.Errorf("全部 %d 条订阅推送失败", failed)
	}
	return nil
}

// SendFromStore 语义同 Send（与 wxpusher/webhook 命名对齐，供巡检/证书提醒调用）。
func SendFromStore(st *store.Store, subject, body string) error {
	return Send(st, subject, body)
}

// AlertNotifier 实现 engine.Notifier：总开关 + 告警细分开关都开才推送。
type AlertNotifier struct{ st *store.Store }

// NewAlertNotifier 创建告警通知器（装配时注入 engine.New）。
func NewAlertNotifier(st *store.Store) *AlertNotifier { return &AlertNotifier{st: st} }

// Notify 推送一条告警消息；开关未开或无订阅时静默跳过。
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
	body := fmt.Sprintf("%s\n%s", ev.Alert.Detail, time.Unix(ev.Alert.UpdatedAt, 0).Format("2006-01-02 15:04:05"))
	if err := send(n.st, subject, body); err != nil {
		log.Printf("告警 Web Push 推送失败: %v", err)
	}
}

// TvOn / SetTvOn 追剧更新细分开关（548，缺省关：口径与邮件提醒一致，用户显式
// 开；渠道总开关仍默认关，两关全开才推送）。
func TvOn(st *store.Store) bool { return switchOn(st, keyTvOn) }
func SetTvOn(st *store.Store, on bool) error {
	return setSwitch(st, keyTvOn, on)
}

// AssistOn / SetAssistOn 个人助理细分开关（早报/天气预警/带伞/空气提醒共用；
// 缺省开，渠道总开关仍默认关，两关全开才推送）。
func AssistOn(st *store.Store) bool { return switchOnDefault(st, keyAssistOn) }
func SetAssistOn(st *store.Store, on bool) error {
	return setSwitch(st, keyAssistOn, on)
}
