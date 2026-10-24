package api

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// maxDashCharts 总览自定义图表数量上限（两行 × 每行 3 个）。
const maxDashCharts = 6

// chartMetrics 允许的指标集；true 表示必须带 param（网口）。
// net = 网络速率，一个图表里同时画下行与上行两条线。
// 磁盘占用率仅存最新（无历史曲线），不再支持自定义图表。
var chartMetrics = map[string]bool{
	"cpu": false, "mem": false, "swap": false,
	"load1": false, "load5": false, "load15": false,
	"tcp_est": false, "net": true, "temp": false,
}

// chartMetricLabel 指标中文名（图表标题用）。
var chartMetricLabel = map[string]string{
	"cpu": "CPU", "mem": "内存", "swap": "交换分区",
	"load1": "负载1分", "load5": "负载5分", "load15": "负载15分",
	"tcp_est": "TCP连接", "net": "网络", "temp": "温度",
}

// normalizeChartMetric 旧配置 net_rx/net_tx 统一并入 net（上下行合并图）。
func normalizeChartMetric(m string) string {
	if m == "net_rx" || m == "net_tx" {
		return "net"
	}
	return m
}

// DashChartHandler 总览页自定义图表：配置 CRUD + 一次性取全部图表数据。
type DashChartHandler struct {
	st *store.Store
}

type dashChartView struct {
	ID          int64  `json:"id"`
	ServerID    int64  `json:"server_id"`
	ServerName  string `json:"server_name"`
	Metric      string `json:"metric"`
	Param       string `json:"param"`
	MetricLabel string `json:"metric_label"`
}

// List 返回全部图表配置（附服务器名，删除服务器时级联清理，正常不缺）。
func (h *DashChartHandler) List(c *gin.Context) {
	charts, err := h.st.ListDashCharts()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询图表失败"})
		return
	}
	names := map[int64]string{}
	out := make([]dashChartView, 0, len(charts))
	for _, ch := range charts {
		if normalizeChartMetric(ch.Metric) == "disk" {
			continue // 磁盘历史已移除（仅存最新状态），旧配置跳过展示
		}
		name, ok := names[ch.ServerID]
		if !ok {
			if srv, err := h.st.GetServer(ch.ServerID); err == nil && srv != nil {
				name = srv.Name
			}
			names[ch.ServerID] = name
		}
		out = append(out, dashChartView{
			ID: ch.ID, ServerID: ch.ServerID, ServerName: name,
			Metric: ch.Metric, Param: ch.Param, MetricLabel: chartMetricLabel[normalizeChartMetric(ch.Metric)],
		})
	}
	c.JSON(http.StatusOK, gin.H{"charts": out})
}

type createDashChartReq struct {
	ServerID int64  `json:"server_id" binding:"required"`
	Metric   string `json:"metric" binding:"required"`
	Param    string `json:"param"`
}

// Create 新增图表：校验服务器存在、指标合法、需要 param 的指标（磁盘/网卡）
// 必须携带、总数不超过上限。
func (h *DashChartHandler) Create(c *gin.Context) {
	var req createDashChartReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	needParam, ok := chartMetrics[req.Metric]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的指标"})
		return
	}
	req.Param = strings.TrimSpace(req.Param)
	if needParam && req.Param == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该指标需要指定挂载点或网口"})
		return
	}
	if srv, err := h.st.GetServer(req.ServerID); err != nil || srv == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "服务器不存在"})
		return
	}
	if n, err := h.st.CountDashCharts(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询图表数失败"})
		return
	} else if n >= maxDashCharts {
		c.JSON(http.StatusBadRequest, gin.H{"error": "最多配置 6 个图表"})
		return
	}
	ch, err := h.st.CreateDashChart(req.ServerID, req.Metric, req.Param)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	invalidateDashChartCache() // 配置已变，旧缓存不再对应图表列表
	c.JSON(http.StatusOK, gin.H{"chart": dashChartView{
		ID: ch.ID, ServerID: ch.ServerID, Metric: ch.Metric, Param: ch.Param,
		MetricLabel: chartMetricLabel[ch.Metric],
	}})
}

// Delete 删除一个图表。
func (h *DashChartHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的图表 ID"})
		return
	}
	if err := h.st.DeleteDashChart(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	invalidateDashChartCache()
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// dashChartRanges 总览图表允许的时段（秒）：5 分钟 ~ 90 天共 14 档
// （与前端 DIALOG_RANGES 一致；白名单外的 range 静默回落 1 小时）。
var dashChartRanges = map[int64]bool{
	300: true, 900: true, 1800: true, // 5/15/30 分钟
	3600: true, 21600: true, 43200: true, 86400: true, // 1/6/12/24 小时
	172800: true, 259200: true, 604800: true, 1209600: true, // 2/3/7/14 天
	2592000: true, 5184000: true, 7776000: true, // 30/60/90 天
}

// dashChartSeries / dashChartData 是 Data 的响应结构。
type dashChartSeries struct {
	Name   string   `json:"name"` // 空 = 单序列，前端用图表标题兜底
	Points [][2]any `json:"points"`
	Avg5m  *float64 `json:"avg5m,omitempty"` // 近5分钟均值（bps，仅网络序列）
}

type dashChartData struct {
	ID       int64             `json:"id"`
	ServerID int64             `json:"server_id"` // 前端点击图表标题跳转服务器详情用
	Name     string            `json:"name"`
	Percent  bool              `json:"percent"`
	Bps      bool              `json:"bps"`
	Series   []dashChartSeries `json:"series"`
}

// dashChartCache 是 Data 的短缓存：总览自动刷新（最快 10s 一轮）反复打同一
// range 的全量聚合查询，大时段一次要范围扫描十几万行且库是单连接——10s 内
// 直接复用上次结果。图表配置增删/重排时失效（见 invalidateDashChartCache）。
var dashChartCache = struct {
	mu              sync.Mutex
	key             int64
	at              time.Time
	from, to, step  int64
	charts          []dashChartData
	queried, served int // 简单计数（调试用）：真实查询次数 / 缓存命中次数
}{}

const dashChartCacheTTL = 10 * time.Second

func invalidateDashChartCache() {
	dashChartCache.mu.Lock()
	dashChartCache.key = 0
	dashChartCache.mu.Unlock()
}

// Data 一次返回全部图表的曲线点（总览页整体刷新只打一个请求）。
// range 取 dashChartRanges 白名单（缺省 1 小时）；步长按区间自动取档
// （约 240 点封顶，比详情页粗，概览够用且数据量小）。同一服务器的序列只查一次。
func (h *DashChartHandler) Data(c *gin.Context) {
	rangeSec := int64(3600)
	if v, err := strconv.ParseInt(c.Query("range"), 10, 64); err == nil && dashChartRanges[v] {
		rangeSec = v
	}
	// 自定义时间段：from/to 同时给定时优先于 range（总览「自定义时间段」查询）。
	// 合法域与档位一致：跨度 5 分钟~90 天、to 不晚于当前 5 分钟；不进缓存。
	customFrom, customTo := int64(0), int64(0)
	if f, err1 := strconv.ParseInt(c.Query("from"), 10, 64); err1 == nil {
		if t, err2 := strconv.ParseInt(c.Query("to"), 10, 64); err2 == nil &&
			f < t && t-f >= 300 && t-f <= 7776000 && t-time.Now().Unix() <= 300 {
			customFrom, customTo = f, t
			rangeSec = t - f
		}
	}
	if customFrom == 0 {
		dashChartCache.mu.Lock()
		if dashChartCache.key == rangeSec && time.Since(dashChartCache.at) < dashChartCacheTTL {
			from, to, step, out := dashChartCache.from, dashChartCache.to, dashChartCache.step, dashChartCache.charts
			dashChartCache.served++
			dashChartCache.mu.Unlock()
			c.JSON(http.StatusOK, gin.H{"from": from, "to": to, "step": step, "charts": out})
			return
		}
		dashChartCache.mu.Unlock()
	}

	charts, err := h.st.ListDashCharts()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询图表失败"})
		return
	}
	to := time.Now().Unix()
	from := to - rangeSec
	if customTo > 0 {
		to, from = customTo, customFrom
	}
	want := rangeSec / 240 // 30 分钟→10s 档 180 点；1h→30s 档；14 天→2 小时档 168 点；90 天→1 天档 90 点
	step := stepTiers[len(stepTiers)-1]
	for _, t := range stepTiers {
		if t >= want {
			step = t
			break
		}
	}

	names := map[int64]string{}
	cache := map[int64]map[string][][2]any{}         // server_id → seriesKey → points
	avgCache := map[int64]map[string]*store.NetAvg{} // server_id → iface → 近5分钟均值
	// 该服务器是否有网卡图：没有则 historySeries 跳过 SampledRows
	// （第二次范围扫描 + 逐桶 nets JSON 解析），标量图省一半查询。
	needNets := map[int64]bool{}
	for _, ch := range charts {
		if m := normalizeChartMetric(ch.Metric); m == "net" || m == "temp" {
			needNets[ch.ServerID] = true // 两类都走 SampledRows（温度在 thermal JSON 列）
		}
	}
	out := make([]dashChartData, 0, len(charts))
	for _, ch := range charts {
		// 旧配置（net_rx/net_tx）自动并入合并后的网络图，同一张图双线展示
		metric := normalizeChartMetric(ch.Metric)
		if metric == "disk" {
			continue // 磁盘历史已移除（仅存最新状态），旧配置跳过
		}
		name, ok := names[ch.ServerID]
		if !ok {
			if srv, err := h.st.GetServer(ch.ServerID); err == nil && srv != nil {
				name = srv.Name
			}
			names[ch.ServerID] = name
		}
		sm, ok := cache[ch.ServerID]
		if !ok {
			series, err := historySeries(h.st, ch.ServerID, from, to, step, needNets[ch.ServerID])
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "查询曲线失败"})
				return
			}
			sm = make(map[string][][2]any, len(series))
			for _, s := range series {
				sm[s.Key] = s.Points
			}
			cache[ch.ServerID] = sm
		}
		title := name + " · " + chartMetricLabel[metric]
		if ch.Param != "" {
			title += "（" + ch.Param + "）"
		}
		cd := dashChartData{
			ID:       ch.ID,
			ServerID: ch.ServerID,
			Name:     title,
			Percent:  metric == "cpu" || metric == "mem" || metric == "swap",
			Bps:      metric == "net",
		}
		switch metric {
		case "net": // 上下行合并为一张双线图，附近5分钟均值
			av, ok := avgCache[ch.ServerID]
			if !ok {
				av, _ = h.st.NetAvgSince(ch.ServerID, to-300) // 失败仅缺省，不影响曲线
				if av == nil {
					av = map[string]*store.NetAvg{}
				}
				avgCache[ch.ServerID] = av
			}
			cd.Series = []dashChartSeries{
				{Name: "下行", Points: sm["net."+ch.Param+".rx"]},
				{Name: "上行", Points: sm["net."+ch.Param+".tx"]},
			}
			if a := av[ch.Param]; a != nil {
				rx, tx := a.Rx, a.Tx
				cd.Series[0].Avg5m, cd.Series[1].Avg5m = &rx, &tx
			}
		case "temp": // param 空=CPU 温度；param=传感器名（sda / nvme0n1 …）=该传感器
			sensor := ch.Param
			if sensor == "" {
				sensor = "CPU"
			}
			cd.Series = []dashChartSeries{{Name: sensor, Points: sm["temp."+sensor]}}
		default:
			cd.Series = []dashChartSeries{{Points: sm[metric]}}
		}
		out = append(out, cd)
	}
	if customTo == 0 { // 自定义窗口不定形，不写缓存（避免污染档位缓存键）
		dashChartCache.mu.Lock()
		dashChartCache.key = rangeSec
		dashChartCache.at = time.Now()
		dashChartCache.from, dashChartCache.to, dashChartCache.step = from, to, step
		dashChartCache.charts = out
		dashChartCache.queried++
		dashChartCache.mu.Unlock()
	}
	c.JSON(http.StatusOK, gin.H{"from": from, "to": to, "step": step, "charts": out})
}

// Order 保存拖拽后的图表顺序（ids 按新顺序传入，写入 sort 1..n）。
func (h *DashChartHandler) Order(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if len(req.IDs) > maxDashCharts {
		c.JSON(http.StatusBadRequest, gin.H{"error": "数量超出上限"})
		return
	}
	if err := h.st.ReorderDashCharts(req.IDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存顺序失败"})
		return
	}
	invalidateDashChartCache()
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
