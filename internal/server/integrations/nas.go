// NAS 集成（441）：群晖 DSM（CGI 登录 + 系统信息/利用率）与 TrueNAS SCALE
// （REST API v2）。DSM 取完即 best-effort logout 释放会话（DSM 后台会话有上限）。
package integrations

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"lz-monitor/internal/server/store"
)

// fetchDSM 抓取群晖系统概览：SYNO.API.Auth 登录换 sid → SYNO.DSM.Info getinfo
// （温度/运行时长/版本）+ SYNO.Core.System.Utilization get（CPU/内存）→ logout。
func fetchDSM(in store.Integration) (map[string]any, error) {
	cred := ParseCredentials(in.Auth)
	if cred.Username == "" || cred.Password == "" {
		return nil, fmt.Errorf("请填写 DSM 账号密码（建议专用受限账号）")
	}
	cl := httpClientFor(configBool(in.Config, "insecure"))
	base := strings.TrimSuffix(in.URL, "/")

	// 登录（format=sid：sid 以应答返回，后续请求 _sid 参数携带）
	var login struct {
		Success bool `json:"success"`
		Data    struct {
			SID string `json:"sid"`
		} `json:"data"`
	}
	if err := postFormJSON(cl, base+"/webapi/entry.cgi", nil, urlValues{
		"api": {"SYNO.API.Auth"}, "version": {"6"}, "method": {"login"},
		"account": {cred.Username}, "passwd": {cred.Password},
		"session": {"PicoPanel"}, "format": {"sid"},
	}, &login); err != nil {
		return nil, fmt.Errorf("连接 DSM 失败: %w", err)
	}
	if !login.Success || login.Data.SID == "" {
		return nil, fmt.Errorf("DSM 登录失败（检查账号密码；开了双重验证的账号不支持，请用专用应用密码或关闭 2FA）")
	}
	sid := login.Data.SID
	cgi := func(api, version, method string, out any) error {
		_, err := getJSON(cl, base+"/webapi/entry.cgi?"+urlValues{
			"api": {api}, "version": {version}, "method": {method}, "_sid": {sid},
		}.Encode(), nil, out)
		return err
	}

	var info struct {
		Success bool `json:"success"`
		Data    struct {
			Temperature int64  `json:"temperature"`
			Uptime      string `json:"uptime"`
			Version     string `json:"version"`
		} `json:"data"`
	}
	if err := cgi("SYNO.DSM.Info", "2", "getinfo", &info); err != nil {
		dsmLogout(cl, base, sid)
		return nil, fmt.Errorf("获取 DSM 系统信息失败: %w", err)
	}
	if !info.Success {
		dsmLogout(cl, base, sid)
		return nil, fmt.Errorf("DSM 拒绝了系统信息请求（检查账号权限）")
	}
	var util struct {
		Success bool `json:"success"`
		Data    struct {
			CPU struct {
				TotalLoad string `json:"total_load"`
			} `json:"cpu"`
			Memory struct {
				RealUsage float64 `json:"real_usage"`
				TotalReal int64   `json:"total_real"`
				UsedReal  int64   `json:"used_real"`
			} `json:"memory"`
		} `json:"data"`
	}
	if err := cgi("SYNO.Core.System.Utilization", "1", "get", &util); err != nil {
		dsmLogout(cl, base, sid)
		return nil, fmt.Errorf("获取 DSM 利用率失败: %w", err)
	}
	if !util.Success {
		dsmLogout(cl, base, sid)
		return nil, fmt.Errorf("DSM 拒绝了利用率请求（检查账号权限）")
	}
	dsmLogout(cl, base, sid) // best-effort：失败不影响快照

	cpu, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(util.Data.CPU.TotalLoad), "%"), 64)
	mem := util.Data.Memory.RealUsage
	if mem == 0 && util.Data.Memory.TotalReal > 0 {
		mem = float64(util.Data.Memory.UsedReal) / float64(util.Data.Memory.TotalReal) * 100
	}
	return map[string]any{
		"cpu_pct":     cpu,
		"mem_pct":     mem,
		"temp_c":      info.Data.Temperature, // 传感器缺失时上游给 -1，前端隐藏
		"uptime_days": uptimeDays(info.Data.Uptime),
		"version":     shortVersion(strings.TrimSpace(info.Data.Version)),
	}, nil
}

// dsmLogout 释放 DSM 会话（错误忽略）。
func dsmLogout(cl *http.Client, base, sid string) {
	_, _ = cl.Get(base + "/webapi/entry.cgi?" + urlValues{
		"api": {"SYNO.API.Auth"}, "version": {"6"}, "method": {"logout"},
		"session": {"PicoPanel"}, "_sid": {sid},
	}.Encode())
}

var uptimeDayRe = regexp.MustCompile(`(\d+)\s*day`)

// uptimeDays 从 DSM uptime 文案（如 "5 days 3 hrs 21 mins"）取整天数；不足一天为 0。
func uptimeDays(s string) int64 {
	m := uptimeDayRe.FindStringSubmatch(strings.ToLower(s))
	if m == nil {
		return 0
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	return n
}

// fetchTrueNAS 抓取 TrueNAS SCALE 概览：Bearer API Key——/api/v2.0/system/info
// （版本/运行秒数/负载）+ /api/v2.0/pool（存储池健康计数）。
func fetchTrueNAS(in store.Integration) (map[string]any, error) {
	cred := ParseCredentials(in.Auth)
	if cred.Token == "" {
		return nil, fmt.Errorf("请填写 TrueNAS API Key（用户→API Keys）")
	}
	cl := httpClientFor(configBool(in.Config, "insecure"))
	base := strings.TrimSuffix(in.URL, "/")
	headers := map[string]string{"Authorization": "Bearer " + cred.Token}

	var info struct {
		Version       string `json:"version"`
		UptimeSeconds int64  `json:"uptime_seconds"`
		Loadavg       any    `json:"loadavg"`
	}
	if _, err := getJSON(cl, base+"/api/v2.0/system/info", headers, &info); err != nil {
		if httpStatus(err) == http.StatusUnauthorized {
			return nil, fmt.Errorf("TrueNAS API Key 无效")
		}
		return nil, fmt.Errorf("获取 TrueNAS 系统信息失败（CORE 老版本走 WebSocket，本集成仅支持 SCALE）: %w", err)
	}
	var pools []struct {
		Status string `json:"status"`
	}
	if _, err := getJSON(cl, base+"/api/v2.0/pool", headers, &pools); err != nil {
		return nil, fmt.Errorf("获取 TrueNAS 存储池失败: %w", err)
	}
	var healthy, total int64
	for _, p := range pools {
		total++
		if p.Status == "ONLINE" {
			healthy++
		}
	}
	ver := info.Version
	for _, prefix := range []string{"TrueNAS-SCALE-", "TrueNAS-CORE-", "TrueNAS-Community-Edition-", "TrueNAS-Enterprise-", "TrueNAS-"} {
		ver = strings.TrimPrefix(ver, prefix)
	}
	return map[string]any{
		"version":       shortVersion(ver),
		"pools_healthy": healthy,
		"pools_total":   total,
		"uptime_days":   info.UptimeSeconds / 86400,
		"load1":         loadAvg1(info.Loadavg),
	}, nil
}

// loadAvg1 负载均值取 1 分钟档（各版本形状不一：{"1min":0.5} 或 ["0.5",...]）。
func loadAvg1(v any) float64 {
	switch x := v.(type) {
	case map[string]any:
		return numAt(x, "1min")
	case []any:
		if len(x) > 0 {
			f, _ := asF64(x[0])
			return f
		}
	}
	return 0
}
