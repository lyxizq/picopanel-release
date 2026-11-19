// Package mihomo mihomo 控制台（TODO 7）：经通用中转任务通道读写 mihomo
// (Clash Meta) external-controller REST API。服务端把每次读/写组装成一条
// net_tasks 任务（purpose=mihomo），由用户选定的 push agent 在内网代执行
// （mihomo 常与 agent 同机，走 127.0.0.1:9090 即可，external-controller 无需
// 暴露公网），结果回传后本包按 ref_key 解析成前端业务数据。
//
// 配置（出口 agent / 控制器地址 / secret / 测延迟 URL）存 settings KV；secret
// 加密落库（SetSettingSealed，seal 口径同 net_tasks URL），派发时注入
// Authorization: Bearer 头。metacubexd 的实时流面板（/traffic /logs 等 WS）
// 中转通道做不了，本包只覆盖 REST 面：概览/代理组/连接/规则 + 切节点/测延迟/
// 断连接/切模式/重载。
package mihomo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"lz-monitor/internal/server/store"
)

// Purpose 中转任务用途（net_tasks.purpose）：agent 侧对 mihomo 前缀放行
// 私网/回环 IP 目标（internal/agent/nettask.go netTaskHostAllowed）。
const Purpose = "mihomo"

// settings KV 键（secret 走加密口径）。
const (
	keyServerID = "mihomo_server_id"
	keyBaseURL  = "mihomo_base_url"
	keySecret   = "mihomo_secret"
	keyTestURL  = "mihomo_test_url"
)

// DefaultTestURL 节点测延迟用 URL（mihomo 默认值，可在设置覆盖）。
const DefaultTestURL = "http://www.gstatic.com/generate_204"

// Config 控制台配置（Secret 不序列化给前端）。
type Config struct {
	ServerID int64  `json:"server_id"`
	BaseURL  string `json:"base_url"`
	Secret   string `json:"-"`
	TestURL  string `json:"test_url"`
}

// LoadConfig 读配置（未配置项零值；TestURL 空回落 DefaultTestURL）。
func LoadConfig(st *store.Store) *Config {
	cfg := &Config{TestURL: DefaultTestURL}
	if v, ok, _ := st.GetSetting(keyServerID); ok {
		cfg.ServerID, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok, _ := st.GetSetting(keyBaseURL); ok {
		cfg.BaseURL = v
	}
	if v, ok, _ := st.GetSettingSealed(keySecret); ok {
		cfg.Secret = v
	}
	if v, ok, _ := st.GetSetting(keyTestURL); ok && v != "" {
		cfg.TestURL = v
	}
	return cfg
}

// SaveConfig 落库。secret 为空串表示「沿用现值不覆盖」（口径同 B 站 SESSDATA
// 的不回显留空沿用），传 replaceSecret=true 且空串则清除。
func SaveConfig(st *store.Store, cfg *Config, replaceSecret bool) error {
	if err := st.SetSetting(keyServerID, strconv.FormatInt(cfg.ServerID, 10)); err != nil {
		return err
	}
	if err := st.SetSetting(keyBaseURL, cfg.BaseURL); err != nil {
		return err
	}
	if err := st.SetSetting(keyTestURL, cfg.TestURL); err != nil {
		return err
	}
	if replaceSecret {
		return st.SetSettingSealed(keySecret, cfg.Secret)
	}
	return nil
}

// Ready 配置是否齐备（齐备才能入队任务）。
func Ready(cfg *Config) bool {
	return cfg != nil && cfg.ServerID > 0 && cfg.BaseURL != ""
}

// Op 单次对 mihomo 的请求面（读面板或写操作）：net_tasks 的一条封装。
type Op struct {
	RefKey  string // purpose+ref_key 活跃唯一：读面板按面、写操作按目标
	Method  string
	Path    string // 拼在控制器地址后，以 / 开头（节点名已 PathEscape）
	Query   url.Values
	Body    string
	Timeout int // 秒；0 = net_tasks 缺省 15
}

// 读面板五面（前端轮询端按 ref_key 复用活跃任务）。
func VersionOp() Op { return Op{RefKey: "version", Method: "GET", Path: "/version"} }
func OverviewOp() Op {
	return Op{RefKey: "overview", Method: "GET", Path: "/configs"}
}
func ProxiesOp() Op { return Op{RefKey: "proxies", Method: "GET", Path: "/proxies"} }
func ConnectionsOp() Op {
	return Op{RefKey: "connections", Method: "GET", Path: "/connections"}
}
func RulesOp() Op { return Op{RefKey: "rules", Method: "GET", Path: "/rules"} }

// 写操作（204 空 body 为成功；delay 类 200 带 JSON）。
func SwitchOp(group, name string) Op {
	return Op{RefKey: "ops:switch:" + group, Method: "PUT",
		Path: "/proxies/" + pathEscape(group), Body: `{"name":` + jsonStr(name) + `}`}
}
func DelayOp(name, testURL string) Op {
	return Op{RefKey: "ops:delay:" + name, Method: "GET",
		Path: "/proxies/" + pathEscape(name) + "/delay", Query: delayQuery(testURL)}
}
func GroupDelayOp(group, testURL string) Op {
	return Op{RefKey: "ops:group_delay:" + group, Method: "GET",
		Path: "/group/" + pathEscape(group) + "/delay", Query: delayQuery(testURL)}
}
func CloseConnOp(id string) Op {
	return Op{RefKey: "ops:close:" + id, Method: "DELETE", Path: "/connections/" + pathEscape(id)}
}
func CloseAllConnsOp() Op {
	return Op{RefKey: "ops:close_all", Method: "DELETE", Path: "/connections"}
}
func ModeOp(mode string) Op {
	return Op{RefKey: "ops:mode", Method: "PATCH", Path: "/configs",
		Body: `{"mode":` + jsonStr(mode) + `}`}
}
func ReloadOp() Op {
	// PUT /configs?force=true 且 path 为空 = 从原配置文件热重载（mihomo 口径）
	return Op{RefKey: "ops:reload", Method: "PUT", Path: "/configs",
		Query:  url.Values{"force": {"true"}},
		Body:   `{"path":"","payload":""}`,
		Timeout: 25}
}

func pathEscape(s string) string { return url.PathEscape(s) }
func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
func delayQuery(testURL string) url.Values {
	if testURL == "" {
		testURL = DefaultTestURL
	}
	return url.Values{"timeout": {"5000"}, "url": {testURL}}
}

// Enqueue 入队一条 mihomo 中转任务（URL 拼接 + secret 注入 Authorization 头）。
func Enqueue(st *store.Store, cfg *Config, op Op) (int64, error) {
	if !Ready(cfg) {
		return 0, errors.New("mihomo 未配置（出口机器或控制器地址缺失）")
	}
	full := strings.TrimRight(cfg.BaseURL, "/") + op.Path
	if len(op.Query) > 0 {
		full += "?" + op.Query.Encode()
	}
	var headers map[string]string
	if cfg.Secret != "" {
		headers = map[string]string{"Authorization": "Bearer " + cfg.Secret}
	}
	return st.CreateNetTask(Purpose, op.RefKey, cfg.ServerID, full, op.Method, headers, op.Body, op.Timeout, false)
}

// ---------- 结果解析（终态任务 → 前端业务数据） ----------

// ParseResult 按 ref_key 解析回传。读面板/写操作统一在服务端裁剪：剔除对前端
// 无用的重字段（history 全量/extra/uid 等），控制响应体。
func ParseResult(refKey string, statusCode int, body, errMsg string) (any, error) {
	if errMsg != "" {
		return nil, errors.New(errMsg)
	}
	if strings.HasPrefix(refKey, "ops:") {
		return parseOpResult(refKey, statusCode, body)
	}
	if statusCode != 200 {
		return nil, fmt.Errorf("mihomo 返回 HTTP %d", statusCode)
	}
	switch refKey {
	case "version":
		var v struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			return nil, err
		}
		return ginH{"version": v.Version}, nil
	case "overview":
		return parseOverview(body)
	case "proxies":
		return parseProxies(body)
	case "connections":
		return parseConnections(body)
	case "rules":
		return parseRules(body)
	}
	return nil, fmt.Errorf("未知的 mihomo 任务面: %s", refKey)
}

// ginH 轻量别名（不引 gin 包，保持本包纯逻辑可测）。
type ginH map[string]any

func parseOverview(body string) (any, error) {
	var raw struct {
		Mode      string `json:"mode"`
		LogLevel  string `json:"log-level"`
		AllowLAN  bool   `json:"allow-lan"`
		MixedPort int    `json:"mixed-port"`
		Tun       struct {
			Enable bool `json:"enable"`
		} `json:"tun"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, errors.New("mihomo 配置响应解析失败")
	}
	return ginH{
		"mode": raw.Mode, "log_level": raw.LogLevel, "allow_lan": raw.AllowLAN,
		"mixed_port": raw.MixedPort, "tun": raw.Tun.Enable,
	}, nil
}

// proxiesNode/proxiesGroup 前端展示裁剪形态。
type proxiesNode struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	UDP   bool   `json:"udp"`
	Alive bool   `json:"alive"`
	Delay int64  `json:"delay"` // 最近一次测延迟（0=未测/失败）
}
type proxiesGroup struct {
	Name  string   `json:"name"`
	Type  string   `json:"type"`
	Now   string   `json:"now"`
	All   []string `json:"all"`
	Delay int64    `json:"delay"` // 组自身（URLTest 等）最近延迟
}

func parseProxies(body string) (any, error) {
	var raw struct {
		Proxies map[string]struct {
			Type    string `json:"type"`
			UDP     bool   `json:"udp"`
			Alive   bool   `json:"alive"`
			Hidden  bool   `json:"hidden"`
			Now     string `json:"now"`
			All     []any  `json:"all"`
			History []struct {
				Delay int64 `json:"delay"`
			} `json:"history"`
		} `json:"proxies"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, errors.New("mihomo 代理响应解析失败")
	}
	nodes := ginH{}
	var groups []proxiesGroup
	for name, p := range raw.Proxies {
		lastDelay := lastDelay(p.History)
		if len(p.All) == 0 {
			nodes[name] = proxiesNode{Name: name, Type: p.Type, UDP: p.UDP, Alive: p.Alive, Delay: lastDelay}
			continue
		}
		all := make([]string, 0, len(p.All))
		for _, a := range p.All {
			if s, ok := a.(string); ok {
				all = append(all, s)
			}
		}
		if p.Hidden {
			continue // Meta 折叠组：不展示
		}
		groups = append(groups, proxiesGroup{Name: name, Type: p.Type, Now: p.Now, All: all, Delay: lastDelay})
	}
	// JSON map 无序：组按 名称 排序给个稳定序（前端可再按类型分组）
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return ginH{"groups": groups, "nodes": nodes}, nil
}

func lastDelay(h []struct {
	Delay int64 `json:"delay"`
}) int64 {
	if len(h) == 0 {
		return 0
	}
	return h[len(h)-1].Delay
}

// maxConnections 连接明细裁剪上限（超出行丢弃但 total 保留真实数；防大几百
// 连接的机器把轮询响应撑肥）。
const maxConnections = 500

func parseConnections(body string) (any, error) {
	var raw struct {
		DownloadTotal int64 `json:"downloadTotal"`
		UploadTotal   int64 `json:"uploadTotal"`
		Memory        int64 `json:"memory"`
		Connections   []struct {
			ID       string `json:"id"`
			Upload   int64  `json:"upload"`
			Download int64  `json:"download"`
			Start    string `json:"start"`
			Chains   []any  `json:"chains"`
			Rule     string `json:"rule"`
			RulePay  string `json:"rulePayload"`
			Metadata struct {
				Network  string `json:"network"`
				Host     string `json:"host"`
				DestIP   string `json:"destinationIP"`
				DestPort string `json:"destinationPort"`
				SrcIP    string `json:"sourceIP"`
				SrcPort  string `json:"sourcePort"`
				ProcPath string `json:"processPath"`
			} `json:"metadata"`
		} `json:"connections"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, errors.New("mihomo 连接响应解析失败")
	}
	conns := raw.Connections
	total := len(conns)
	if total > maxConnections {
		conns = conns[:maxConnections]
	}
	out := make([]ginH, 0, len(conns))
	for _, c := range conns {
		chains := make([]string, 0, len(c.Chains))
		for i := len(c.Chains) - 1; i >= 0; i-- { // 反转：规则组 → … → 出口节点
			if s, ok := c.Chains[i].(string); ok {
				chains = append(chains, s)
			}
		}
		host := c.Metadata.Host
		if host == "" {
			host = c.Metadata.DestIP
		}
		// process 空路径时 path.Base 会返回 "."（394 用户报连接列表每行多个点），
		// 无进程信息就回空串；source 同理无 IP:端口时回空串
		proc := ""
		if c.Metadata.ProcPath != "" {
			if b := path.Base(c.Metadata.ProcPath); b != "." && b != "/" {
				proc = b
			}
		}
		src := ""
		if c.Metadata.SrcIP != "" || c.Metadata.SrcPort != "" {
			src = strings.TrimPrefix(c.Metadata.SrcIP+":"+c.Metadata.SrcPort, ":")
		}
		out = append(out, ginH{
			"id": c.ID, "network": c.Metadata.Network, "host": host,
			"dest": strings.TrimPrefix(c.Metadata.DestIP+":"+c.Metadata.DestPort, ":"),
			"source": src,
			"process": proc,
			"rule":    strings.TrimSpace(c.Rule + " " + c.RulePay),
			"chains":  chains, "upload": c.Upload, "download": c.Download, "start": c.Start,
		})
	}
	return ginH{
		"download_total": raw.DownloadTotal, "upload_total": raw.UploadTotal,
		"memory": raw.Memory, "total": total, "connections": out,
	}, nil
}

// maxRules 规则清单返回上限（存量匹配树几千条也只回前 2000，total 保留真实数）。
const maxRules = 2000

func parseRules(body string) (any, error) {
	var raw struct {
		Rules []struct {
			Type    string `json:"type"`
			Payload string `json:"payload"`
			Proxy   string `json:"proxy"`
			Size    int64  `json:"size"`
		} `json:"rules"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, errors.New("mihomo 规则响应解析失败")
	}
	total := len(raw.Rules)
	if total > maxRules {
		raw.Rules = raw.Rules[:maxRules]
	}
	return ginH{"total": total, "rules": raw.Rules}, nil
}

// parseOpResult 写操作回传：2xx=成功（204 无 body 常态）；delay 类带 JSON。
func parseOpResult(refKey string, statusCode int, body string) (any, error) {
	kind := strings.TrimPrefix(refKey, "ops:")
	switch {
	case strings.HasPrefix(kind, "delay:"):
		if statusCode != 200 {
			return nil, fmt.Errorf("测延迟失败（HTTP %d）", statusCode)
		}
		var v struct {
			Delay int64 `json:"delay"`
		}
		if err := json.Unmarshal([]byte(body), &v); err != nil || v.Delay <= 0 {
			return nil, errors.New("节点超时")
		}
		return ginH{"delay": v.Delay}, nil
	case strings.HasPrefix(kind, "group_delay:"):
		if statusCode != 200 {
			return nil, fmt.Errorf("组测延迟失败（HTTP %d）", statusCode)
		}
		return ginH{"delays": parseDelayMap(body)}, nil
	}
	if statusCode >= 200 && statusCode < 300 {
		return ginH{"ok": true}, nil
	}
	msg := ""
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(body), &e) == nil {
		msg = e.Message
	}
	if msg != "" {
		return nil, errors.New(msg)
	}
	return nil, fmt.Errorf("操作失败（HTTP %d）", statusCode)
}

// parseDelayMap 组测延迟响应：成功节点为数值；失败节点 Meta 回 {message:...}
// 对象（老版本回 0），统一记 0。
func parseDelayMap(body string) map[string]int64 {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return map[string]int64{}
	}
	out := make(map[string]int64, len(raw))
	for k, v := range raw {
		var d int64
		if json.Unmarshal(v, &d) == nil && d > 0 {
			out[k] = d
		} else {
			out[k] = 0
		}
	}
	return out
}
