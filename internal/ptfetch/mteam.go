package ptfetch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// M-Team 官方 API（391，口径参考 PT-depiler 的 mteam 定义）：x-api-key 鉴权，
// ①POST /api/member/profile → 用户名/等级/上传/下载/魔力/入站时间；
// ②POST /api/tracker/myPeerStatistics → 做种数/做种量；
// ③POST /api/tracker/mybonus → 时魔值（formulaParams.finalBs）。

// mteamLevelNames role id → 等级名（M-Team 等级表）。
var mteamLevelNames = map[int64]string{
	0: "Peasant", 1: "User", 2: "Power User", 3: "Elite User", 4: "Crazy User",
	5: "Insane User", 6: "Veteran User", 7: "Extreme User", 8: "Ultimate User",
	9: "mTorrent Master", 10: "VIP",
}

// mteamAPIBase M-Team 官方 API 自 2024-06 起统一走 api. 子域，Web 域名（kp.
// 等）的 /api 路径由源站 nginx 直接回 403。把 m-team 系基址的主机首段改写为
// api（已是 api. 原样）；非 m-team 域（单测 httptest 桩）不动。
func mteamAPIBase(webBase string) string {
	u, err := url.Parse(webBase)
	if err != nil || u.Host == "" {
		return webBase
	}
	h := u.Hostname()
	if strings.HasPrefix(h, "api.") || !strings.Contains(h, "m-team") {
		return webBase
	}
	dot := strings.Index(h, ".")
	if dot <= 0 {
		return webBase
	}
	if p := u.Port(); p != "" {
		u.Host = "api." + h[dot+1:] + ":" + p
	} else {
		u.Host = "api." + h[dot+1:]
	}
	return strings.TrimRight(u.String(), "/")
}

// mteamPost 调一个 API 端点（POST，x-api-key 头），解析业务包络：
// M-Team 对无效 Key 也可能回 200，业务错误在 JSON 的 message/data 里，一并带出。
// origin 传 Web 基址（2025-10 上游加了按 Origin 的 CORS 校验，浏览器插件同款）。
func mteamPost(base, origin, path, apiKey, ua string, out any) error {
	req, err := http.NewRequest("POST", joinURL(base, path), bytes.NewReader([]byte{}))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("x-api-key", apiKey)
	resp, err := sameHostClient().Do(req)
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d（%s）", resp.StatusCode, req.URL)
	}
	var env struct {
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("响应不是 JSON: %s", truncateForLog(string(body), 200))
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		msg := env.Message
		if msg == "" {
			msg = truncateForLog(string(body), 200)
		}
		return fmt.Errorf("上游返回错误: %s", msg)
	}
	return json.Unmarshal(env.Data, out)
}

// mteamStr / mteamF64 宽松取数：上游字段在 string/number 间摇摆（实测 role
// 是字符串、uploaded 是数字），两型都试。
func mteamStr(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func mteamF64(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case string:
		return parseNumStr(v)
	}
	return 0
}

func mteamMap(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

// fetchMTeam 抓 M-Team 用户信息。base 是 Web 基址（卡片外链用），API 调用
// 走 mteamAPIBase 改写出的 api. 子域。
func fetchMTeam(base, apiKey, ua string) (*Snapshot, error) {
	if strings.TrimSpace(apiKey) == "" {
		// 空 key 上游回「Full authentication is required」，不如直说人话
		return nil, fmt.Errorf("未配置 API Key（编辑站点填入存取令牌）")
	}
	snap := &Snapshot{Seeding: -1}
	apiBase := mteamAPIBase(base)

	var profile map[string]any
	if err := mteamPost(apiBase, base, "/api/member/profile", apiKey, ua, &profile); err != nil {
		return nil, fmt.Errorf("用户资料: %w", err)
	}
	snap.Username = mteamStr(profile, "username")
	if snap.Username == "" {
		snap.Username = mteamStr(profile, "id") // 极端兜底：至少有标识
	}
	snap.Uploaded = int64(mteamF64(profile, "uploaded"))
	snap.Downloaded = int64(mteamF64(profile, "downloaded"))
	if snap.Downloaded > 0 {
		snap.Ratio = float64(snap.Uploaded) / float64(snap.Downloaded)
	}
	snap.Bonus = mteamF64(profile, "bonus")
	role := mteamStr(profile, "role")
	if role == "" {
		role = mteamStr(profile, "class")
	}
	if id, err := strconv.ParseInt(role, 10, 64); err == nil {
		if name, ok := mteamLevelNames[id]; ok {
			snap.Level = name
		} else {
			snap.Level = "Lv." + role
		}
	} else {
		snap.Level = role
	}
	if mc := mteamMap(profile, "memberCount"); mc != nil {
		if v := mteamF64(mc, "uploaded"); v > 0 {
			snap.Uploaded = int64(v)
		}
		if v := mteamF64(mc, "downloaded"); v > 0 {
			snap.Downloaded = int64(v)
			snap.Ratio = float64(snap.Uploaded) / float64(snap.Downloaded)
		}
		if v := mteamF64(mc, "bonus"); v > 0 {
			snap.Bonus = v
		}
	}
	if created := mteamStr(profile, "createdDate"); created != "" {
		snap.JoinedAt = parseTimeStr(created)
	}

	time.Sleep(nexusStepGap)

	var peer map[string]any
	if err := mteamPost(apiBase, base, "/api/tracker/myPeerStatistics", apiKey, ua, &peer); err == nil {
		snap.Seeding = int64(mteamF64(peer, "seederCount"))
		snap.SeedingSz = int64(mteamF64(peer, "seederSize"))
	} else {
		snap.Seeding = -1
	}

	time.Sleep(nexusStepGap)

	var bonus map[string]any
	if err := mteamPost(apiBase, base, "/api/tracker/mybonus", apiKey, ua, &bonus); err == nil {
		if fp := mteamMap(bonus, "formulaParams"); fp != nil {
			if v := mteamF64(fp, "finalBs"); v > 0 {
				snap.BonusPH = &v
			}
		}
	}
	return snap, nil
}

// truncateForLog 日志/报错里的响应截断。
func truncateForLog(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
