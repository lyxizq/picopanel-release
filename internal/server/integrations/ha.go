// Home Assistant 客户端（Homepage widget 同口径）：长效访问令牌 + REST API。
// 一次 GET /api/states 全量拉回实体状态，按配置的实体列表过滤出展示行——
// N 个实体只发 1 个请求；实体未找到显示 "-"，state 原样展示（HA 的 state
// 可以是任意字符串：on/home/45.3 等）。
package integrations

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lz-monitor/internal/server/store"
)

// HAEntity config.entities 一项：展示配置。
type HAEntity struct {
	Label    string `json:"label"`    // 展示名（空=实体友好名/实体 ID）
	Entity   string `json:"entity"`   // entity_id（sensor.temperature 等）
	Unit     string `json:"unit"`     // 单位（空=跟随实体 unit_of_measurement）
	Decimals *int   `json:"decimals"` // 小数位数（nil=原样；0-4，数值型状态才生效，268）
}

// HADevice config.devices 一项：可控设备（355，/ha 页面磁贴）。icon_on/icon_off
// 为自定义图标图片 URL（空=按域名用前端内置图形）。
type HADevice struct {
	Entity  string `json:"entity"`
	Label   string `json:"label"`
	Room    string `json:"room,omitempty"` // 所属房间 id（371，空=未分组）
	IconOn  string `json:"icon_on,omitempty"`
	IconOff string `json:"icon_off,omitempty"`
}

// HASensor config.sensors 一项：/ha 页面状态实体（368）——温度/电量等只读
// 展示项，state 与单位直接取 /api/states 快照，配置里不落值。
type HASensor struct {
	Entity string `json:"entity"`
	Label  string `json:"label"`
	Room   string `json:"room,omitempty"` // 所属房间 id（371，空=未分组）
}

// HARoom config.rooms 一项：/ha 页面房间分区（371）——设备/状态实体按 room id
// 归房展示，未归房的收「未分组」节；不配房间则维持平铺。
type HARoom struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// HAConfig config 列结构：{entities:[...], devices:[...], sensors:[...], rooms:[...]}。
type HAConfig struct {
	Entities []HAEntity `json:"entities"`
	Devices  []HADevice `json:"devices"`
	Sensors  []HASensor `json:"sensors"`
	Rooms    []HARoom   `json:"rooms"`
}

// ParseHAConfig 解析 HA 配置（空/坏 JSON 返回空配置）。
func ParseHAConfig(config string) HAConfig {
	var c HAConfig
	if config != "" {
		_ = jsonUnmarshal(config, &c)
	}
	return c
}

// haControllableDomains 可控域白名单（355）：/ha 页面只展示/只允许调用这些域，
// sensor/binary_sensor/scene/script 等只读或无开关语义的实体一律挡在外面。
var haControllableDomains = map[string]bool{
	"light": true, "switch": true, "fan": true, "climate": true, "cover": true,
	"humidifier": true, "media_player": true, "lock": true, "vacuum": true,
	"water_heater": true, "input_boolean": true, "siren": true,
}

// HAControllableDomain 实体是否属于可控域。
func HAControllableDomain(entityID string) bool {
	d, _, _ := strings.Cut(entityID, ".")
	return haControllableDomains[d]
}

// haDomainServices 各域开/关对应的 HA 服务名（域内 turn_on/turn_off 不可用的
// 换真服务：cover 开合帘、lock 上锁解锁、vacuum 启停）；默认 turn_on/turn_off。
var haDomainServices = map[string][2]string{
	"cover":  {"open_cover", "close_cover"},
	"lock":   {"lock", "unlock"},
	"vacuum": {"start", "stop"},
}

// haUnavailableStates 一律按「离线」展示的 state 取值（HA 约定小写）。
var haUnavailableStates = map[string]bool{"": true, "unavailable": true, "unknown": true}

// HAState 单个实体当前状态（/ha 页面与目录候选共用）。Cur/Tgt 等环境字段
// （388）仅环境电器域（climate/humidifier/water_heater/fan）有值，其余实体为 nil。
type HAState struct {
	EntityID string `json:"entity_id"`
	Domain   string `json:"domain"`
	State    string `json:"state"`
	Name     string `json:"name"`
	Unit     string `json:"unit,omitempty"`
	// 环境电器属性透传（388）：当前/目标温度、当前/目标湿度、运行动作、
	// 风速档、风速百分比、预设/模式、可调范围。
	CurTemp    *float64 `json:"cur_temp,omitempty"`
	TgtTemp    *float64 `json:"tgt_temp,omitempty"`
	CurHum     *float64 `json:"cur_hum,omitempty"`
	TgtHum     *float64 `json:"tgt_hum,omitempty"`
	HvacAction string   `json:"hvac_action,omitempty"`
	FanMode    string   `json:"fan_mode,omitempty"`
	Pct        *float64 `json:"pct,omitempty"`
	Preset     string   `json:"preset,omitempty"`
	MinTemp    *float64 `json:"min_temp,omitempty"`
	MaxTemp    *float64 `json:"max_temp,omitempty"`
	MinHum     *float64 `json:"min_hum,omitempty"`
	MaxHum     *float64 `json:"max_hum,omitempty"`
}

// On 设备是否处于「开」态：各域语义不同（cover=open/opening、lock=unlocked、
// climate/media_player=非 off 类），其余域看 on/off。
func (s HAState) On() bool {
	if s.Unavailable() {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(s.State))
	switch s.Domain {
	case "cover":
		return v == "open" || v == "opening"
	case "lock":
		return v == "unlocked" || v == "unlocking"
	case "vacuum":
		return v == "cleaning" || v == "on" || v == "returning"
	case "climate":
		return v != "off"
	case "media_player":
		return v != "off" && v != "idle" && v != "standby"
	default:
		return v == "on"
	}
}

// Unavailable 实体是否离线/状态未知。
func (s HAState) Unavailable() bool {
	return haUnavailableStates[strings.ToLower(strings.TrimSpace(s.State))]
}

// haServiceFor 返回 domain 上执行开/关动作的服务名。action ∈ turn_on/turn_off。
func haServiceFor(domain, action string) string {
	if svcs, ok := haDomainServices[domain]; ok {
		if action == "turn_off" {
			return svcs[1]
		}
		return svcs[0]
	}
	return action
}

// fetchHA 抓取一帧 Home Assistant 快照。Data 两个键：
//   - items：主页展示行（config.entities 过滤，口径不变）
//   - states：全部实体的当前状态表（368 扩为全量，原 355 只收可控域），
//     map[entity_id]HAState——/ha 设备行、状态实体行与目录候选共用
func fetchHA(in store.Integration) (map[string]any, error) {
	cred := ParseCredentials(in.Auth)
	if cred.Token == "" {
		return nil, fmt.Errorf("未配置长效访问令牌（HA 用户资料页最下方生成）")
	}
	cfg := ParseHAConfig(in.Config)
	cl := httpClientFor(configBool(in.Config, "insecure"))
	base := strings.TrimSuffix(in.URL, "/")
	headers := map[string]string{"Authorization": "Bearer " + cred.Token}

	var states []haStatePayload
	if _, err := getJSON(cl, base+"/api/states", headers, &states); err != nil {
		return nil, fmt.Errorf("获取实体状态失败: %w", err)
	}
	// states：全部实体的当前状态表（368 扩为全量：可控域实体供 /ha 设备行与
	// 调用白名单，sensor 等只读实体供状态实体展示与目录候选），map[entity_id]HAState。
	// 环境属性按域归位（388）：climate/water_heater 温度、humidifier 湿度（HA
	// 口径：climate 的 temperature=目标、current_temperature=当前；humidifier 的
	// humidity=目标、current_humidity=当前）、fan 只透传风速百分比与预设。
	byID := make(map[string]HAState, len(states))
	for _, st := range states {
		s := haStateFromPayload(st)
		byID[s.EntityID] = s
	}

	items := make([]any, 0, len(cfg.Entities))
	for _, e := range cfg.Entities {
		if strings.TrimSpace(e.Entity) == "" {
			continue
		}
		label, value, unit := e.Label, "-", e.Unit
		if s, ok := byID[e.Entity]; ok {
			if label == "" {
				label = s.Name
			}
			value = s.State
			if unit == "" {
				unit = s.Unit
			}
		} else if label == "" {
			label = e.Entity // 实体不在线且无自定义名：以 entity_id 兜底，值保持 "-"
		}
		// 小数位（268）：配置了位数且状态可解析为数值时按位数四舍五入
		if e.Decimals != nil && *e.Decimals >= 0 && *e.Decimals <= 4 {
			if f, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
				value = strconv.FormatFloat(f, 'f', *e.Decimals, 64)
			}
		}
		items = append(items, map[string]any{
			"label": label,
			"value": value,
			"unit":  unit,
		})
	}
	return map[string]any{"items": items, "states": byID}, nil
}

// haStatePayload /api/states/<id> 单实体应答解码结构（fetchHA 全量与 HAGetState
// 单拉共用同一段属性字段，388 增环境属性）。
type haStatePayload struct {
	EntityID   string `json:"entity_id"`
	State      string `json:"state"`
	Attributes struct {
		FriendlyName       string   `json:"friendly_name"`
		UnitOfMeasurement  string   `json:"unit_of_measurement"`
		CurrentTemperature *float64 `json:"current_temperature"`
		Temperature        *float64 `json:"temperature"`
		CurrentHumidity    *float64 `json:"current_humidity"`
		Humidity           *float64 `json:"humidity"`
		HvacAction         string   `json:"hvac_action"`
		FanMode            string   `json:"fan_mode"`
		Percentage         *float64 `json:"percentage"`
		PresetMode         string   `json:"preset_mode"`
		Mode               string   `json:"mode"`
		MinTemp            *float64 `json:"min_temp"`
		MaxTemp            *float64 `json:"max_temp"`
		MinHumidity        *float64 `json:"min_humidity"`
		MaxHumidity        *float64 `json:"max_humidity"`
	} `json:"attributes"`
}

// haStateFromPayload 解码结果 → HAState，环境属性按域归位（口径同 fetchHA）。
func haStateFromPayload(st haStatePayload) HAState {
	domain, _, _ := strings.Cut(st.EntityID, ".")
	s := HAState{
		EntityID: st.EntityID, Domain: domain, State: st.State,
		Name: st.Attributes.FriendlyName, Unit: st.Attributes.UnitOfMeasurement,
	}
	switch domain {
	case "climate", "water_heater":
		s.CurTemp = st.Attributes.CurrentTemperature
		s.TgtTemp = st.Attributes.Temperature
		s.CurHum = st.Attributes.CurrentHumidity
		s.HvacAction = st.Attributes.HvacAction
		s.FanMode = st.Attributes.FanMode
		s.Preset = st.Attributes.PresetMode
		s.MinTemp = st.Attributes.MinTemp
		s.MaxTemp = st.Attributes.MaxTemp
	case "humidifier":
		s.CurHum = st.Attributes.CurrentHumidity
		s.TgtHum = st.Attributes.Humidity
		s.Preset = st.Attributes.Mode
		s.MinHum = st.Attributes.MinHumidity
		s.MaxHum = st.Attributes.MaxHumidity
	case "fan":
		s.Pct = st.Attributes.Percentage
		s.Preset = st.Attributes.PresetMode
	}
	return s
}

// HAGetState 拉取单个实体当前状态（开关后读回、toggle 前判向共用）。
func HAGetState(in store.Integration, entityID string) (*HAState, error) {
	cred := ParseCredentials(in.Auth)
	if cred.Token == "" {
		return nil, fmt.Errorf("未配置长效访问令牌")
	}
	cl := httpClientFor(configBool(in.Config, "insecure"))
	base := strings.TrimSuffix(in.URL, "/")
	var st haStatePayload
	hdr := map[string]string{"Authorization": "Bearer " + cred.Token}
	if _, err := getJSON(cl, base+"/api/states/"+entityID, hdr, &st); err != nil {
		return nil, err
	}
	s := haStateFromPayload(st)
	return &s, nil
}

// HACall 开关一台设备：action ∈ turn_on/turn_off/toggle。toggle 先实时读一次
// 状态判向（缓存可能滞后，直接转发 toggle 有翻错方向的风险），再按域映射真
// 服务（cover/lock/vacuum 无 turn_on/turn_off）。返回读回后的最新状态——HA
// 服务调用本身返回空体且状态落库有约 1s 延迟，这里调用后紧接一次 GET 把
// 最终状态带回，前端免二次轮询。
func HACall(in store.Integration, entityID, action string) (*HAState, error) {
	if !HAControllableDomain(entityID) {
		return nil, fmt.Errorf("不支持的实体域")
	}
	if action != "turn_on" && action != "turn_off" && action != "toggle" {
		return nil, fmt.Errorf("无效的动作")
	}
	if action == "toggle" {
		cur, err := HAGetState(in, entityID)
		if err != nil {
			return nil, fmt.Errorf("读取当前状态失败: %w", err)
		}
		if cur.On() {
			action = "turn_off"
		} else {
			action = "turn_on"
		}
	}
	cred := ParseCredentials(in.Auth)
	if cred.Token == "" {
		return nil, fmt.Errorf("未配置长效访问令牌")
	}
	cl := httpClientFor(configBool(in.Config, "insecure"))
	base := strings.TrimSuffix(in.URL, "/")
	domain, _, _ := strings.Cut(entityID, ".")
	svc := haServiceFor(domain, action)
	body := fmt.Sprintf(`{"entity_id":%q}`, entityID)
	req, err := http.NewRequest(http.MethodPost, base+"/api/services/"+domain+"/"+svc,
		strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cred.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用服务失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("调用服务返回 %s", resp.Status)
	}
	st, err := HAGetState(in, entityID)
	if err != nil {
		return nil, fmt.Errorf("调用成功但读回状态失败: %w", err)
	}
	return st, nil
}

// haDomainSetService 环境电器目标值调节（388）按域映射的服务与参数名：
// climate/water_heater 调目标温度、humidifier 调目标湿度。
var haDomainSetService = map[string][2]string{
	"climate":      {"set_temperature", "temperature"},
	"water_heater": {"set_temperature", "temperature"},
	"humidifier":   {"set_humidity", "humidity"},
}

// HASettableDomain 实体是否支持目标值调节（388：climate/water_heater/humidifier）。
func HASettableDomain(entityID string) bool {
	d, _, _ := strings.Cut(entityID, ".")
	_, ok := haDomainSetService[d]
	return ok
}

// HASetValue 调节环境电器目标值（388）：按域映射真服务（climate/water_heater
// 的 set_temperature、humidifier 的 set_humidity），其余域拒绝；返回读回后的
// 最新状态（含环境属性，前端步进器直接取新目标值）。
func HASetValue(in store.Integration, entityID string, value float64) (*HAState, error) {
	domain, _, _ := strings.Cut(entityID, ".")
	svc, ok := haDomainSetService[domain]
	if !ok {
		return nil, fmt.Errorf("该实体不支持目标值调节")
	}
	cred := ParseCredentials(in.Auth)
	if cred.Token == "" {
		return nil, fmt.Errorf("未配置长效访问令牌")
	}
	cl := httpClientFor(configBool(in.Config, "insecure"))
	base := strings.TrimSuffix(in.URL, "/")
	body := fmt.Sprintf(`{"entity_id":%q,%q:%v}`, entityID, svc[1], value)
	req, err := http.NewRequest(http.MethodPost, base+"/api/services/"+domain+"/"+svc[0],
		strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cred.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用服务失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("调用服务返回 %s", resp.Status)
	}
	st, err := HAGetState(in, entityID)
	if err != nil {
		return nil, fmt.Errorf("调用成功但读回状态失败: %w", err)
	}
	return st, nil
}

// ---- 开关日志与数值采样观察（511/512，2026-10-09）----

// haLogDomains 记开合日志的域（可控设备集合；域内开/关语义统一走 HAState.On）。
var haLogDomains = map[string]bool{
	"light": true, "switch": true, "fan": true, "climate": true, "humidifier": true,
	"water_heater": true, "cover": true, "lock": true, "vacuum": true, "media_player": true,
}

// 门窗类传感器关键词（口径同前端 HaView DOOR_KW/DOOR_EXC，406）：开合翻转也记
// 日志（2026-10-09 用户指定「门的开关也记录」）。
var haDoorKW = regexp.MustCompile(`door|window|门|窗`)
var haDoorExc = regexp.MustCompile(`indoor|outdoor|铃|bell`)

// observeHAFrame 抓帧后的增量观察：与上一帧基线比对记开合翻转（ha_events），
// sensor 域数值态按单位白名单记采样（ha_samples）。首帧只立基线不记事件（服务
// 重启同理，防重启刷一批假事件）；任一侧离线只更新基线（防离线抖动误记）。
// 失败只落日志，不影响抓帧主链路。
func (m *Manager) observeHAFrame(in store.Integration, p Payload) {
	if !p.OK || in.Type != "homeassistant" {
		return
	}
	states, _ := p.Data["states"].(map[string]HAState)
	if states == nil {
		return
	}
	now := time.Now().Unix()
	// 基线比对/更新/落库全程持锁：observe 会并发进入（Kick 协程/轮询循环/页面
	// 补拉各自独立触发），只锁外层取引用的话 prev 读写裸奔——并发写 map 是进程
	// 级 fatal。落库也必须同临界区：否则并发两帧的翻转顺序与插入顺序可能颠倒，
	// 同秒事件按 id 排序会把日志排乱。写库毫秒级，持锁代价可忽略。
	m.haMu.Lock()
	prev := m.haPrev[in.ID]
	if prev == nil {
		prev = map[string]string{}
		m.haPrev[in.ID] = prev
	}
	var events []store.HAEvent
	var samples []store.HASample
	for id, st := range states {
		if st.Domain == "sensor" {
			if v, ok := haSampleValue(st); ok {
				samples = append(samples, store.HASample{EntityID: id, Value: v, TS: now})
			}
		}
		if !haLoggable(st) {
			continue
		}
		if st.Unavailable() {
			continue // 离线：不动基线不记事件（恢复后与离线前状态比对）
		}
		norm := "off"
		if st.On() {
			norm = "on"
		}
		if old, ok := prev[id]; ok && old != norm {
			events = append(events, store.HAEvent{EntityID: id, Name: st.Name, From: old, To: norm, TS: now})
		}
		prev[id] = norm
	}
	if len(events) > 0 || len(samples) > 0 {
		if err := m.st.RecordHAFrame(events, samples); err != nil {
			log.Printf("记录 HA 开关日志/采样失败: %v", err)
		}
	}
	m.haMu.Unlock()
}

// haLoggable 实体是否记开合日志：可控域，或门窗类传感器/二值传感器
// （实体 id + 友好名命中关键词且不落排除词）。
func haLoggable(st HAState) bool {
	if haLogDomains[st.Domain] {
		return true
	}
	if st.Domain != "sensor" && st.Domain != "binary_sensor" {
		return false
	}
	s := st.Name + " " + st.EntityID
	return haDoorKW.MatchString(s) && !haDoorExc.MatchString(s)
}

// haSampleValue sensor 数值态按单位白名单取采样值（512）：°C/°F 温度、%
// 湿度与电池电量（2026-10-09 用户点名电量含电池，红框芯片点曲线）、
// W/kW 功率、kWh/Wh/MWh 电量；其余单位或非数值态不采。
func haSampleValue(st HAState) (float64, bool) {
	switch strings.ToLower(strings.TrimSpace(st.Unit)) {
	case "°c", "°f", "%", "w", "kw", "kwh", "wh", "mwh":
	default:
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(st.State), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
