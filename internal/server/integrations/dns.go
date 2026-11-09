// DNS 拦截类集成（436）：AdGuard Home 与 Pi-hole 的拦截统计快照。
// 统一产出 queries/blocked/blocked_pct，第四格 AdGuard 为平均延迟、Pi-hole 为
// 拦截清单（gravity）规模。开放部署允许不填凭据（AdGuard Basic 头有值才带）。
package integrations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"lz-monitor/internal/server/store"
)

// fetchAdGuard 抓取 AdGuard Home 统计：GET /control/stats（Basic 认证，可选）。
// 统计窗口跟随上游配置（默认 24h），拦截率为本地按 拦截/查询 求出。
func fetchAdGuard(in store.Integration) (map[string]any, error) {
	cred := ParseCredentials(in.Auth)
	cl := httpClientFor(configBool(in.Config, "insecure"))
	base := strings.TrimSuffix(in.URL, "/")
	headers := map[string]string{}
	if cred.Username != "" || cred.Password != "" {
		headers["Authorization"] = basicAuth(cred.Username, cred.Password)
	}
	var stats struct {
		Queries     int64   `json:"num_dns_queries"`
		Blocked     int64   `json:"num_blocked_filtering"`
		AvgProcTime float64 `json:"avg_processing_time"`
	}
	if _, err := getJSON(cl, base+"/control/stats", headers, &stats); err != nil {
		return nil, fmt.Errorf("获取 AdGuard 统计失败: %w", err)
	}
	return map[string]any{
		"queries":     stats.Queries,
		"blocked":     stats.Blocked,
		"blocked_pct": pctOf(stats.Blocked, stats.Queries),
		"avg_ms":      stats.AvgProcTime * 1000,
	}, nil
}

// pctOf 百分比（一位小数），分母为 0 时返回 0。
func pctOf(part, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

// errPiholeV5 上游不认识 v6 API（404/405），应回落 v5 api.php。
var errPiholeV5 = errors.New("pihole: v5")

// fetchPihole 抓取 Pi-hole 统计：先按 v6（POST /api/auth 换 sid →
// /api/stats/summary），上游 404/405 时回落 v5（/api.php?summary，auth 参数为
// 双 SHA256 后的管理密码；无凭据直接匿名拉，适配开放部署）。
func fetchPihole(in store.Integration) (map[string]any, error) {
	cred := ParseCredentials(in.Auth)
	cl := httpClientFor(configBool(in.Config, "insecure"))
	base := strings.TrimSuffix(in.URL, "/")

	data, err := piholeV6(cl, base, cred.Token)
	if errors.Is(err, errPiholeV5) {
		return piholeV5(cl, base, cred.Token)
	}
	return data, err
}

func piholeV6(cl *http.Client, base, password string) (map[string]any, error) {
	headers := map[string]string{}
	if password != "" {
		var auth struct {
			Session struct {
				SID   string `json:"sid"`
				Valid bool   `json:"valid"`
			} `json:"session"`
		}
		err := postJSON(cl, base+"/api/auth", nil, map[string]string{"password": password}, &auth)
		if err != nil {
			code := httpStatus(err)
			if code == http.StatusNotFound || code == http.StatusMethodNotAllowed {
				return nil, errPiholeV5
			}
			if code == http.StatusUnauthorized {
				return nil, fmt.Errorf("Pi-hole 管理密码错误")
			}
			return nil, fmt.Errorf("Pi-hole v6 认证失败: %w", err)
		}
		if auth.Session.SID != "" {
			// v6 文档允许 X-FTL-SID 与 Bearer 两种携带方式，双发兼容各小版本
			headers["X-FTL-SID"] = auth.Session.SID
			headers["Authorization"] = "Bearer " + auth.Session.SID
		}
	}
	var sum map[string]any
	if _, err := getJSON(cl, base+"/api/stats/summary", headers, &sum); err != nil {
		if httpStatus(err) == http.StatusUnauthorized {
			return nil, fmt.Errorf("Pi-hole 需要管理密码（v6 默认开启 API 认证）")
		}
		return nil, fmt.Errorf("获取 Pi-hole 统计失败: %w", err)
	}
	queries := mapAny(sum["queries"])
	blocked := numAt(queries, "blocked")
	total := numAt(queries, "total")
	if total == 0 {
		total = blocked + numAt(queries, "valid") // 个别版本无 total 字段
	}
	gravity := mapAny(sum["gravity"])
	return map[string]any{
		"queries":     int64(total),
		"blocked":     int64(blocked),
		"blocked_pct": pctOf(int64(blocked), int64(total)),
		"gravity":     int64(numAt(gravity, "domains_being_blocked")),
	}, nil
}

func piholeV5(cl *http.Client, base, password string) (map[string]any, error) {
	url := base + "/api.php?summary"
	if password != "" {
		// v5 auth 参数 = webpassword = sha256(sha256(明文))（Pi-hole 前端口径）
		url += "&auth=" + sha256Hex(sha256Hex(password))
	}
	resp, err := cl.Get(url)
	if err != nil {
		return nil, fmt.Errorf("连接 Pi-hole 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取 Pi-hole 统计失败: HTTP %d", resp.StatusCode)
	}
	var raw map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("应答解析失败: %w", err)
	}
	// v5 summary 数值全是字符串
	queries, _ := asF64(raw["dns_queries_today"])
	blocked, _ := asF64(raw["ads_blocked_today"])
	pct, hasPct := asF64(raw["ads_percentage_today"])
	if !hasPct {
		pct = pctOf(int64(blocked), int64(queries))
	}
	gravity, _ := asF64(raw["domains_being_blocked"])
	return map[string]any{
		"queries":     int64(queries),
		"blocked":     int64(blocked),
		"blocked_pct": pct,
		"gravity":     int64(gravity),
	}, nil
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// mapAny 宽容取嵌套对象（缺键/类型不符返回空 map）。
func mapAny(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// numAt 从对象里宽容取数值键。
func numAt(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	f, _ := asF64(m[key])
	return f
}
