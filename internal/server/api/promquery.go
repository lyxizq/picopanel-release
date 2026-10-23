package api

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// PromQueryHandler 提供只读即时查询：GET /api/v1/query?query=lz_cpu{server="web-01"}。
// 请求与响应格式模仿 Prometheus instant query，Homepage 的 prometheusmetric 小部件
// 可直接对接（widget.url 填 lz-server 地址即可）。与快照接口的区别：每行指标独立
// 查询，只返回命中的一个小数值而非整台机器的快照，一个面板条目里可混排多台服务器
// 的指标。与 /api/v1/snapshot 共用 snapshot_token。令牌两通道：X-Snapshot-Token 头
// （curl/脚本首选，不落日志）或查询串 token 标签——该小部件不支持自定义请求头，
// 标签是它唯一的携带方式，代价是令牌会随 URL 落访问/反代日志（README 已警示）。
type PromQueryHandler struct {
	st           *store.Store
	token        string
	offlineAfter int64
}

// lzMetrics 支持的指标名（lz_ 前缀，避免与真实 Prometheus 指标混淆），含义见 README。
var lzMetrics = map[string]bool{
	"lz_cpu": true, "lz_mem": true, "lz_mem_used": true, "lz_mem_total": true,
	"lz_swap": true, "lz_load1": true, "lz_load5": true, "lz_load15": true,
	"lz_disk": true, "lz_io": true, "lz_net_rx": true, "lz_net_tx": true,
	"lz_tcp_est": true, "lz_uptime": true, "lz_online": true, "lz_firing": true,
}

// promVector / promSample 对齐 Prometheus /api/v1/query 的 vector 响应封装，
// Homepage 小部件取 data.result[0].value[1] 作为显示值。
type promVector struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string       `json:"resultType"`
		Result     []promSample `json:"result"`
	} `json:"data"`
}

type promSample struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"` // [Unix 秒, 数值字符串]
}

// Instant 处理一次即时查询。
func (h *PromQueryHandler) Instant(c *gin.Context) {
	name, labels, err := parseInstantQuery(c.Query("query"))
	if err != nil {
		promError(c, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	if !lzMetrics[name] {
		promError(c, http.StatusBadRequest, "bad_data", "未知指标: "+name)
		return
	}
	// 令牌两通道：查询串 token 标签（prometheusmetric 小部件唯一可用）与请求头
	got := labels["token"]
	if got == "" {
		got = c.GetHeader("X-Snapshot-Token")
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) != 1 {
		promError(c, http.StatusUnauthorized, "unauthorized", "令牌无效")
		return
	}

	c.Header("Cache-Control", "no-store")
	if labels["server"] == "" { // 未指定 server 无法定位，按无匹配返回空结果
		h.emit(c, nil)
		return
	}
	srv := findServer(h.st, labels["server"])
	if srv == nil {
		h.emit(c, nil)
		return
	}

	var v float64
	var ts int64
	switch name {
	case "lz_online":
		v = bool2f(srv.Enabled && srv.LastReportAt > 0 &&
			time.Now().Unix()-srv.LastReportAt <= h.offlineAfter)
		ts = time.Now().Unix()
	case "lz_firing":
		firing, _ := h.st.FiringCountByServer()
		v, ts = float64(firing[srv.ID]), time.Now().Unix()
	default:
		m, _ := h.st.LatestMetric(srv.ID)
		if m == nil {
			h.emit(c, nil)
			return
		}
		val, ok := metricValue(m, srv, name, labels)
		if !ok {
			h.emit(c, nil)
			return
		}
		v, ts = val, m.TS
	}
	h.emit(c, &promSample{
		Metric: map[string]string{"__name__": name, "server": srv.Name},
		Value:  [2]any{ts, strconv.FormatFloat(v, 'g', -1, 64)},
	})
}

// parseInstantQuery 解析简化 PromQL：lz_<指标>{标签="值",...}（label 块可省略）。
// 标签值不含引号与逗号（服务器名/挂载点/网卡名满足；含逗号的名称请改用数字 ID）。
func parseInstantQuery(q string) (string, map[string]string, error) {
	labels := map[string]string{}
	open := strings.IndexByte(q, '{')
	if open < 0 {
		name := strings.TrimSpace(q)
		if !strings.HasPrefix(name, "lz_") {
			return "", nil, fmt.Errorf("查询必须以 lz_ 指标名开头: %q", q)
		}
		return name, labels, nil
	}
	if !strings.HasSuffix(q, "}") {
		return "", nil, fmt.Errorf("查询缺少右花括号: %q", q)
	}
	name := strings.TrimSpace(q[:open])
	if !strings.HasPrefix(name, "lz_") {
		return "", nil, fmt.Errorf("查询必须以 lz_ 指标名开头: %q", q)
	}
	for _, pair := range strings.Split(q[open+1:len(q)-1], ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			return "", nil, fmt.Errorf("标签无法解析: %q", pair)
		}
		k, val := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
		if len(val) < 2 || val[0] != '"' || val[len(val)-1] != '"' {
			return "", nil, fmt.Errorf("标签值必须加双引号: %q", pair)
		}
		labels[k] = val[1 : len(val)-1]
	}
	return name, labels, nil
}

// metricValue 从最新指标行取值。lz_disk/lz_io 可用 mount 标签选具体挂载点（缺省
// 取最大值）；lz_net_* 可用 device 标签直选单网卡（缺省按服务器配置的 net_ifaces
// 过滤，空=全部）。第二个返回值 false 表示无匹配样本（返回空结果）。
func metricValue(m *store.MetricRow, srv *store.Server, name string, labels map[string]string) (float64, bool) {
	switch name {
	case "lz_cpu":
		return m.CPU, true
	case "lz_mem":
		return m.Mem, true
	case "lz_mem_used":
		return float64(m.MemUsed), true
	case "lz_mem_total":
		return float64(m.MemTotal), true
	case "lz_swap":
		return m.Swap, true
	case "lz_load1":
		return m.Load1, true
	case "lz_load5":
		return m.Load5, true
	case "lz_load15":
		return m.Load15, true
	case "lz_uptime":
		return float64(m.Uptime), true
	case "lz_tcp_est":
		return float64(m.TCPEst), true
	case "lz_net_rx", "lz_net_tx":
		iface := labels["device"]
		if iface == "" {
			iface = srv.NetIfaces // 逗号分隔网卡列表，单网卡名同样适用 sumNet
		}
		rx, tx := sumNet(m, iface)
		if name == "lz_net_rx" {
			return rx, true
		}
		return tx, true
	case "lz_disk", "lz_io":
		want := labels["mount"]
		best := 0.0
		for _, d := range m.Disks {
			pct := d.Percent
			if name == "lz_io" {
				pct = d.IOUtil
			}
			if want != "" {
				if d.Mount == want {
					return pct, true
				}
				continue
			}
			if pct > best {
				best = pct
			}
		}
		if want != "" {
			return 0, false // 指定挂载点不存在（已卸载/改名）
		}
		return best, true
	}
	return 0, false
}

// emit 输出 vector 响应；sample 为 nil 时返回空 result。
func (h *PromQueryHandler) emit(c *gin.Context, sample *promSample) {
	resp := promVector{Status: "success"}
	resp.Data.ResultType = "vector"
	if sample != nil {
		resp.Data.Result = []promSample{*sample}
	}
	c.JSON(http.StatusOK, resp)
}

// promError 按 Prometheus 惯例输出错误响应。
func promError(c *gin.Context, code int, typ, msg string) {
	c.JSON(code, gin.H{"status": "error", "errorType": typ, "error": msg})
}

// bool2f 布尔转 0/1（lz_online 用）。
func bool2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
