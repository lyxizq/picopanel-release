package aiusage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
)

// EdgeOne（腾讯云 TEO，393）用量拉取：手写 TC3-HMAC-SHA256 签名（API 3.0 通用
// 签法，stdlib 够用不引官方 SDK），全部走 POST JSON（Action 在 X-TC-Action 头，
// 根路径）。指标口径对齐控制台「站点数据概览」：总流量/总带宽峰值/总请求数
// （DescribeTimingL7AnalysisData）+ 防护命中次数（DescribeWebProtectionData，
// 仅国际站端点提供）。上游数据约 10 分钟延迟，窗口终点回退对齐。

const (
	edgeOneService = "teo"
	edgeOneVersion = "2022-09-01"
	// edgeOneZoneCap 单次查询站点数上限（接口约束 ZoneIds 最多 100 个，超出分批）。
	edgeOneZoneCap = 100
)

// edgeOneTime 腾云时序接口的 ISO8601 时间格式（带本机时区偏移）。
func edgeOneTime(t time.Time) string {
	return t.Format("2006-01-02T15:04:05Z07:00")
}

// tc3Sign 计算 TC3-HMAC-SHA256 签名，返回 Authorization 头值。
// now 用同一时刻生成 X-TC-Timestamp，日期取 UTC（腾讯云签名字符串规范）。
func tc3Sign(secretID, secretKey, host, action, payload string, now time.Time) string {
	hashed := sha256.Sum256([]byte(payload))
	payloadHex := hex.EncodeToString(hashed[:])
	// 规范请求串：方法/URI/查询串各占一行；CanonicalHeaders 每行自带 \n，
	// 其后再跟一个空行分隔；SignedHeaders 行、payload 哈希行
	canonical := "POST\n/\n\n" +
		"content-type:application/json; charset=utf-8\n" +
		"host:" + host + "\n" +
		"x-tc-action:" + strings.ToLower(action) + "\n" +
		"\n" +
		"content-type;host;x-tc-action\n" +
		payloadHex
	canonicalHash := sha256.Sum256([]byte(canonical))
	date := now.UTC().Format("2006-01-02")
	stringToSign := strings.Join([]string{
		"TC3-HMAC-SHA256",
		strconv.FormatInt(now.Unix(), 10),
		date + "/" + edgeOneService + "/tc3_request",
		hex.EncodeToString(canonicalHash[:]),
	}, "\n")
	kDate := hmacSHA256([]byte("TC3"+secretKey), []byte(date))
	kService := hmacSHA256(kDate, []byte(edgeOneService))
	kSigning := hmacSHA256(kService, []byte("tc3_request"))
	sig := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))
	return fmt.Sprintf("TC3-HMAC-SHA256 Credential=%s/%s/%s/tc3_request, SignedHeaders=content-type;host;x-tc-action, Signature=%s",
		secretID, date, edgeOneService, sig)
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// teoCall 调一次 TEO 接口：业务错误在 HTTP 200 的 Response.Error 里（API 3.0
// 惯例），必须展开判错；out 指向 Response 内层结构。
func teoCall(base, secretID, secretKey, action string, params any, out any) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}
	now := time.Now()
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	req, err := http.NewRequest(http.MethodPost, base, strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", edgeOneVersion)
	req.Header.Set("X-TC-Timestamp", strconv.FormatInt(now.Unix(), 10))
	req.Header.Set("Authorization", tc3Sign(secretID, secretKey, host, action, string(payload), now))
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("上游返回 %d（请求地址 %s）: %s", resp.StatusCode, base, truncate(string(body), 160))
	}
	var env struct {
		Response struct {
			Error *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
			RequestId string `json:"RequestId"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("响应解析失败: %w（原始响应: %s）", err, truncate(string(body), 200))
	}
	if e := env.Response.Error; e != nil {
		// 无效密钥/无权限等都在这里（HTTP 仍是 200），带出 Code 好定位
		// （如 AuthFailure.SignatureFailure / InvalidAction=端点选错站点类型）
		return fmt.Errorf("EdgeOne 业务错误 %s: %s", e.Code, e.Message)
	}
	if out == nil {
		return nil
	}
	var inner struct {
		Response json.RawMessage `json:"Response"`
	}
	if err := json.Unmarshal(body, &inner); err != nil {
		return err
	}
	if err := json.Unmarshal(inner.Response, out); err != nil {
		return fmt.Errorf("响应解析失败: %w（原始响应: %s）", err, truncate(string(body), 200))
	}
	return nil
}

// ---- DescribeZones（站点清单）----

type teoZone struct {
	ZoneID string `json:"ZoneId"`
	Name   string `json:"ZoneName"`
}

// edgeOneZoneIDs 解析账户要查询的站点：zone_ids 配置了就用配置（逗号/空格/分号
// 分隔、去空去重）；没配则 DescribeZones 分页拉账号下全部站点。无站点报错——
// 空账号大概率是密钥有权限问题，不该静默落成全 0 用量。
func edgeOneZoneIDs(base string, a *store.AiAccount) ([]string, error) {
	if ids := ParseZoneIDs(a.ZoneIDs); len(ids) > 0 {
		return ids, nil
	}
	var out []string
	seen := map[string]bool{}
	for offset := 0; offset < 10000; offset += 100 { // 万级站点兜底防死循环
		var r struct {
			TotalCount int       `json:"TotalCount"`
			Zones      []teoZone `json:"Zones"`
		}
		if err := teoCall(base, a.SecretID, a.SecretKey, "DescribeZones",
			map[string]any{"Offset": offset, "Limit": 100}, &r); err != nil {
			return nil, fmt.Errorf("拉取站点列表失败: %w", err)
		}
		for _, z := range r.Zones {
			if z.ZoneID != "" && !seen[z.ZoneID] {
				seen[z.ZoneID] = true
				out = append(out, z.ZoneID)
			}
		}
		if len(r.Zones) < 100 || len(out) >= r.TotalCount {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("账号下没有可用站点（密钥权限或站点过滤问题）")
	}
	return out, nil
}

// ParseZoneIDs 站点 ID 清洗：逗号/空格/分号/换行分隔、去空去重、保序。
// store 存清洗后的逗号串（api 层入库前归一），查询时直接拆用。
func ParseZoneIDs(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' })
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// ---- 时序指标（L7 分析 + Web 防护）----

// teoTimingRecord 时序响应的通用形态（两个数据接口同构）：每条记录是一个维度值
// （TypeKey=站点 ID），下面挂各指标的 Sum/Max 汇总。
type teoTimingRecord struct {
	TypeKey   string `json:"TypeKey"`
	TypeValue []struct {
		MetricName string `json:"MetricName"`
		Sum        int64  `json:"Sum"`
		Max        int64  `json:"Max"`
	} `json:"TypeValue"`
}

// eoWindow 单批站点一个窗口的四指标结果。
type eoWindow struct {
	traffic, peak, requests int64
	hits                    *int64
}

// edgeOneWindow 单批站点在 [start,end] 窗口的四指标。防护命中失败不致命
// （返回 hits=nil，国内站端点无此接口），L7 三指标失败则整窗报错。
func edgeOneWindow(base string, a *store.AiAccount, zones []string, start, end time.Time) (eoWindow, error) {
	zids := zonePtrs(zones)
	metrics := []string{"l7Flow_flux", "l7Flow_bandwidth", "l7Flow_request"}
	var l7 struct {
		Data []teoTimingRecord `json:"Data"`
	}
	err := teoCall(base, a.SecretID, a.SecretKey, "DescribeTimingL7AnalysisData", map[string]any{
		"StartTime": edgeOneTime(start), "EndTime": edgeOneTime(end),
		"MetricNames": metrics, "ZoneIds": zids, "Interval": "5min",
	}, &l7)
	if err != nil {
		return eoWindow{}, fmt.Errorf("拉取七层用量失败: %w", err)
	}
	w := eoWindow{}
	for _, rec := range l7.Data {
		for _, tv := range rec.TypeValue {
			switch tv.MetricName {
			case "l7Flow_flux":
				w.traffic += tv.Sum
			case "l7Flow_bandwidth":
				if tv.Max > w.peak {
					w.peak = tv.Max
				}
			case "l7Flow_request":
				w.requests += tv.Sum
			}
		}
	}
	var prot struct {
		Data []teoTimingRecord `json:"Data"`
	}
	if err := teoCall(base, a.SecretID, a.SecretKey, "DescribeWebProtectionData", map[string]any{
		"StartTime": edgeOneTime(start), "EndTime": edgeOneTime(end),
		"MetricNames": []string{"WebProtectionHit"}, "ZoneIds": zids, "Interval": "5min",
	}, &prot); err != nil {
		logDegraded(a.ID, "防护命中", err)
		return w, nil
	}
	logDegraded(a.ID, "防护命中", nil)
	var sum int64
	for _, rec := range prot.Data {
		for _, tv := range rec.TypeValue {
			if tv.MetricName == "WebProtectionHit" {
				sum += tv.Sum
			}
		}
	}
	w.hits = &sum
	return w, nil
}

// fetchEdgeOne 拉取 EdgeOne 用量：近 24 小时窗口 + 前 24 小时窗口（环比基期，
// 失败静默缺省）。站点数超过单查上限时分批聚合（流量/请求数累加、峰值取大）。
// 详情块（398 弹层）单独 best-effort 拉取，失败只缺省对应字段。
func fetchEdgeOne(a store.AiAccount) (*store.AiUsageSnapshot, error) {
	if a.SecretID == "" || a.SecretKey == "" {
		return nil, fmt.Errorf("缺少 SecretId/SecretKey")
	}
	base := store.AiAccountBaseURL(&a)
	zones, err := edgeOneZoneIDs(base, &a)
	if err != nil {
		return nil, err
	}
	end := time.Now().Add(-10 * time.Minute) // 对齐上游约 10 分钟的数据延迟
	chunks := chunkStrings(zones, edgeOneZoneCap)
	var cur, prev eoAgg
	for _, chunk := range chunks {
		chunk := chunk
		// 当前/前一窗口并行（407：上游全超时最坏 2×15s 而非 4×15s）
		var c, p eoWindow
		var errC, errP error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); c, errC = edgeOneWindow(base, &a, chunk, end.Add(-24*time.Hour), end) }()
		go func() {
			defer wg.Done()
			p, errP = edgeOneWindow(base, &a, chunk, end.Add(-48*time.Hour), end.Add(-24*time.Hour))
		}()
		wg.Wait()
		if errC != nil {
			return nil, errC
		}
		if errP != nil {
			return nil, errP
		}
		cur.add(c)
		prev.add(p)
	}
	stats := &store.EdgeOneStats{
		ZoneCount:     len(zones),
		TrafficBytes:  cur.traffic,
		PeakBandwidth: cur.peak,
		Requests:      cur.requests,
		ProtectHits:   cur.hits,
	}
	if prev.seen {
		stats.PrevTraffic, stats.PrevPeak, stats.PrevRequests, stats.PrevHits =
			&prev.traffic, &prev.peak, &prev.requests, prev.hits
	}
	stats.Detail = fetchEdgeOneDetail(base, &a, zones, end)
	return &store.AiUsageSnapshot{Kind: "edgeone", EdgeOne: stats}, nil
}

// ---- 详情弹层数据（398，各块 best-effort 独立降级）----

// 降级日志去重器（407）：同一账户同一块连续相同的失败只在状态变化时记一条
// 日志——国内站端点没有防护命中/部分详情接口，否则每轮轮询刷 5-6 条
// InvalidAction 噪音。按账户×块记忆上次状态，恢复（成功）也记一条。
var (
	degMu   sync.Mutex
	degSeen = map[int64]map[string]string{}
)

// shouldLogDegraded 判定并更新状态（变化返回 true）。msg 空=该块恢复正常。
func shouldLogDegraded(accID int64, block, msg string) bool {
	degMu.Lock()
	defer degMu.Unlock()
	if degSeen[accID] == nil {
		degSeen[accID] = map[string]string{}
	}
	if degSeen[accID][block] == msg {
		return false
	}
	degSeen[accID][block] = msg
	return true
}

// logDegraded 状态变化时记录一条失败/恢复日志。
func logDegraded(accID int64, block string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if !shouldLogDegraded(accID, block, msg) {
		return
	}
	if err != nil {
		log.Printf("EdgeOne %s 查询失败（降级为空，持续失败不重复记录）: %v", block, err)
	} else {
		log.Printf("EdgeOne %s 查询恢复", block)
	}
}

// clearDegraded 删除账户时清去重状态（避免 ID 复用后首次状态被误吞）。
func clearDegraded(accID int64) {
	degMu.Lock()
	delete(degSeen, accID)
	degMu.Unlock()
}

// fetchEdgeOneDetail 拉弹层详情：缓存命中流量、回源、状态码分布、Top 域名/URL、
// DDoS 峰值。六块相互独立，并发拉取（407：上游全超时最坏 15s 而非 90s）；
// 任何一块失败只让该块缺省（nil），不影响主指标与其他块。
func fetchEdgeOneDetail(base string, a *store.AiAccount, zones []string, end time.Time) *store.EdgeOneDetail {
	start := end.Add(-24 * time.Hour)
	d := &store.EdgeOneDetail{}
	var wg sync.WaitGroup
	// run 在独立 goroutine 里拉一块：成功/失败都经去重器记状态
	run := func(block string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logDegraded(a.ID, block, fn())
		}()
	}

	// 缓存命中流量：l7Flow_hit_outFlux 为国际站专属指标，单独调用——放进
	// 主 L7 查询会让不识别该指标的端点（国内站）整单报错连累主指标
	run("缓存命中", func() error {
		var hit struct {
			Data []teoTimingRecord `json:"Data"`
		}
		if err := teoCall(base, a.SecretID, a.SecretKey, "DescribeTimingL7AnalysisData", map[string]any{
			"StartTime": edgeOneTime(start), "EndTime": edgeOneTime(end),
			"MetricNames": []string{"l7Flow_hit_outFlux"}, "ZoneIds": zonePtrs(zones), "Interval": "5min",
		}, &hit); err != nil {
			return err
		}
		var v int64
		for _, rec := range hit.Data {
			for _, tv := range rec.TypeValue {
				if tv.MetricName == "l7Flow_hit_outFlux" {
					v += tv.Sum
				}
			}
		}
		d.CacheHitFlux = &v
		return nil
	})

	// 回源：请求数 + 节点→源站流量
	run("回源", func() error {
		var origin struct {
			Data []teoTimingRecord `json:"Data"`
		}
		if err := teoCall(base, a.SecretID, a.SecretKey, "DescribeTimingL7OriginPullData", map[string]any{
			"StartTime": edgeOneTime(start), "EndTime": edgeOneTime(end),
			"MetricNames": []string{"l7Flow_request_hy", "l7Flow_outFlux_hy"}, "ZoneIds": zonePtrs(zones), "Interval": "5min",
		}, &origin); err != nil {
			return err
		}
		var req, flux int64
		for _, rec := range origin.Data {
			for _, tv := range rec.TypeValue {
				switch tv.MetricName {
				case "l7Flow_request_hy":
					req += tv.Sum
				case "l7Flow_outFlux_hy":
					flux += tv.Sum
				}
			}
		}
		d.OriginReq, d.OriginFlux = &req, &flux
		return nil
	})

	// TopN（域名/URL/状态码，按请求数）：返回按站点分记录，逐站点明细合并
	// 同名累加后降序取前 10
	top := func(block, metric string, dst *[]store.EdgeOneTopItem) {
		run(block, func() error {
			items, err := edgeOneTopN(base, a, zones, start, end, metric)
			if err != nil {
				return err
			}
			*dst = items
			return nil
		})
	}
	top("Top域名", "l7Flow_request_domain", &d.TopDomains)
	top("TopURL", "l7Flow_request_url", &d.TopURLs)
	top("状态码", "l7Flow_request_statusCode", &d.StatusCodes)

	// 响应流量总值（410）：缓存与回源节展示行 + 流量排行百分比分母
	run("响应流量", func() error {
		var out struct {
			Data []teoTimingRecord `json:"Data"`
		}
		if err := teoCall(base, a.SecretID, a.SecretKey, "DescribeTimingL7AnalysisData", map[string]any{
			"StartTime": edgeOneTime(start), "EndTime": edgeOneTime(end),
			"MetricNames": []string{"l7Flow_outFlux"}, "ZoneIds": zonePtrs(zones), "Interval": "5min",
		}, &out); err != nil {
			return err
		}
		var v int64
		for _, rec := range out.Data {
			for _, tv := range rec.TypeValue {
				if tv.MetricName == "l7Flow_outFlux" {
					v += tv.Sum
				}
			}
		}
		d.OutFlux = &v
		return nil
	})

	// 响应流量排行（410，对齐控制台指标分析）：Host/客户端 IP/Referer/URL Path
	top("Host排行", "l7Flow_outFlux_domain", &d.FluxHosts)
	top("客户端IP", "l7Flow_outFlux_sip", &d.FluxIPs)
	top("Referer", "l7Flow_outFlux_referers", &d.FluxReferers)
	top("URLPath流量", "l7Flow_outFlux_url", &d.FluxURLs)

	// DDoS 攻击峰值：MaxBandwidth（bps）/ MaxPackageRate（pps）时序取最大。
	// 该接口文档明确时间参数为 UTC，按文档发（其余接口发带时区偏移的 ISO8601）
	run("DDoS", func() error {
		var ddos struct {
			Data []teoTimingRecord `json:"Data"`
		}
		if err := teoCall(base, a.SecretID, a.SecretKey, "DescribeDDoSAttackData", map[string]any{
			"StartTime": edgeOneTime(start.UTC()), "EndTime": edgeOneTime(end.UTC()),
			"MetricNames": []string{"ddos_attackMaxBandwidth", "ddos_attackMaxPackageRate"}, "ZoneIds": zonePtrs(zones),
		}, &ddos); err != nil {
			return err
		}
		var bps, pps int64
		for _, rec := range ddos.Data {
			for _, tv := range rec.TypeValue {
				switch tv.MetricName {
				case "ddos_attackMaxBandwidth":
					if tv.Max > bps {
						bps = tv.Max
					}
				case "ddos_attackMaxPackageRate":
					if tv.Max > pps {
						pps = tv.Max
					}
				}
			}
		}
		d.DDoSPeakBps, d.DDoSPeakPps = &bps, &pps
		return nil
	})

	wg.Wait()
	return d
}

// edgeOneTopN 拉一个 TopN 指标并合并排序（前 10）。失败返回 error（该块降级缺省）。
func edgeOneTopN(base string, a *store.AiAccount, zones []string, start, end time.Time, metric string) ([]store.EdgeOneTopItem, error) {
	var r struct {
		Data []struct {
			TypeKey    string `json:"TypeKey"`
			DetailData []struct {
				Key   string `json:"Key"`
				Value int64  `json:"Value"`
			} `json:"DetailData"`
		} `json:"Data"`
	}
	// TopL7 的 MetricName 是单值字符串（非数组），ZoneIds 必填
	if err := teoCall(base, a.SecretID, a.SecretKey, "DescribeTopL7AnalysisData", map[string]any{
		"StartTime": edgeOneTime(start), "EndTime": edgeOneTime(end),
		"MetricName": metric, "ZoneIds": zonePtrs(zones), "Limit": 10,
	}, &r); err != nil {
		return nil, err
	}
	merged := map[string]int64{}
	for _, rec := range r.Data {
		for _, it := range rec.DetailData {
			if it.Key == "" {
				continue
			}
			merged[it.Key] += it.Value
		}
	}
	out := make([]store.EdgeOneTopItem, 0, len(merged))
	for k, v := range merged {
		out = append(out, store.EdgeOneTopItem{Key: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	if len(out) > 10 {
		out = out[:10]
	}
	return out, nil
}

// zonePtrs 站点 ID 转 []*string（接口参数形态）。
func zonePtrs(zones []string) []*string {
	out := make([]*string, len(zones))
	for i, z := range zones {
		z := z
		out[i] = &z
	}
	return out
}

// eoAgg 跨批次聚合器：流量/请求数累加、峰值取大、防护命中任一批缺失即视为缺失。
type eoAgg struct {
	traffic, peak, requests int64
	hits                    *int64
	seen                    bool
}

func (g *eoAgg) add(w eoWindow) {
	g.traffic += w.traffic
	if w.peak > g.peak {
		g.peak = w.peak
	}
	g.requests += w.requests
	if w.hits == nil {
		g.hits = nil
	} else if g.hits != nil {
		v := *g.hits + *w.hits
		g.hits = &v
	} else if g.seen {
		g.hits = nil
	} else {
		g.hits = w.hits
	}
	g.seen = true
}

// chunkStrings 按上限切片。
func chunkStrings(list []string, size int) [][]string {
	if len(list) == 0 {
		return nil
	}
	var out [][]string
	for i := 0; i < len(list); i += size {
		end := i + size
		if end > len(list) {
			end = len(list)
		}
		out = append(out, list[i:end])
	}
	return out
}
