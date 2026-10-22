package store

import (
	"encoding/json"
)

// haMasterSetting settings 键：HA 智能家居页（/ha）的独立总配置（475）。
// 与服务集成的 homeassistant 行完全独立——服务卡走集成行的 entities/open_url，
// 智能家居页走这里（连接参数 + rooms/devices/sensors），两边互不读写。
const haMasterSetting = "ha_master"

// HAMaster 智能家居总配置。Token 出入参均明文，库内整体 JSON 中以 seal 密文存
// （auth 字段，同 integrations.auth 口径）；Rooms/Devices/Sensors 为原样 JSON
// 片段（ParseHAConfig 直接消费，键形同集成 config）。
type HAMaster struct {
	URL      string          `json:"url"`
	Token    string          `json:"-"`
	Insecure bool            `json:"insecure"`
	Rooms    json.RawMessage `json:"rooms"`
	Devices  json.RawMessage `json:"devices"`
	Sensors  json.RawMessage `json:"sensors"`
}

// GetHAMaster 读总配置；从未配置时返回零值结构（URL 空 = 页面显示未配置态）。
func (s *Store) GetHAMaster() (*HAMaster, error) {
	raw, ok, err := s.GetSetting(haMasterSetting)
	if err != nil || !ok {
		return &HAMaster{}, err
	}
	var m struct {
		URL      string          `json:"url"`
		Auth     string          `json:"auth"`
		Insecure bool            `json:"insecure"`
		Rooms    json.RawMessage `json:"rooms"`
		Devices  json.RawMessage `json:"devices"`
		Sensors  json.RawMessage `json:"sensors"`
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return &HAMaster{}, nil // 坏数据按未配置处理，不让整页瘫
	}
	return &HAMaster{URL: m.URL, Token: s.unseal(m.Auth), Insecure: m.Insecure,
		Rooms: m.Rooms, Devices: m.Devices, Sensors: m.Sensors}, nil
}

// SaveHAMasterConnection 保存连接参数（token 空=沿用库内值）。
func (s *Store) SaveHAMasterConnection(url, token string, insecure bool) error {
	m, err := s.GetHAMaster()
	if err != nil {
		return err
	}
	if token == "" {
		token = m.Token
	}
	return s.saveHAMaster(&HAMaster{URL: url, Token: token, Insecure: insecure,
		Rooms: m.Rooms, Devices: m.Devices, Sensors: m.Sensors})
}

// SaveHAMasterLists 保存房间/设备/状态实体清单（/ha/devices-config 链路），
// 连接参数原样保留。三段为调用方 marshal 好的 JSON（nil = 不改该段）。
func (s *Store) SaveHAMasterLists(rooms, devices, sensors json.RawMessage) error {
	m, err := s.GetHAMaster()
	if err != nil {
		return err
	}
	if rooms != nil {
		m.Rooms = rooms
	}
	if devices != nil {
		m.Devices = devices
	}
	if sensors != nil {
		m.Sensors = sensors
	}
	return s.saveHAMaster(m)
}

func (s *Store) saveHAMaster(m *HAMaster) error {
	auth := ""
	if m.Token != "" {
		auth = s.seal(m.Token)
	}
	raw, err := json.Marshal(map[string]any{
		"url": m.URL, "auth": auth, "insecure": m.Insecure,
		"rooms": rawOrEmpty(m.Rooms), "devices": rawOrEmpty(m.Devices), "sensors": rawOrEmpty(m.Sensors),
	})
	if err != nil {
		return err
	}
	return s.SetSetting(haMasterSetting, string(raw))
}

func rawOrEmpty(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage("[]")
	}
	return r
}

// MigrateHAMaster 一次性迁移（开机调用）：settings 无 ha_master 键时，从首个
// HA 集成行拷贝 连接参数+rooms/devices/sensors 到总配置（475 用户指定「拷贝前台
// ha 配置到 ha 智能家居总中」；集成行原样保留，服务卡不受影响）。没有 HA 集成
// 时写 {} 哨兵——防止日后在服务里新建 HA 集成被误当成智能家居的配置来源。
func (s *Store) MigrateHAMaster() error {
	if _, ok, err := s.GetSetting(haMasterSetting); err != nil {
		return err
	} else if ok {
		return nil
	}
	list, err := s.ListIntegrations()
	if err != nil {
		return err
	}
	for i := range list {
		in := &list[i]
		if in.Type != IntegrationHA {
			continue
		}
		cfg := struct {
			Insecure bool            `json:"insecure"`
			Rooms    json.RawMessage `json:"rooms"`
			Devices  json.RawMessage `json:"devices"`
			Sensors  json.RawMessage `json:"sensors"`
		}{}
		_ = json.Unmarshal([]byte(in.Config), &cfg)
		var cred struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal([]byte(in.Auth), &cred)
		return s.saveHAMaster(&HAMaster{URL: in.URL, Token: cred.Token, Insecure: cfg.Insecure,
			Rooms: cfg.Rooms, Devices: cfg.Devices, Sensors: cfg.Sensors})
	}
	return s.SetSetting(haMasterSetting, "{}")
}
