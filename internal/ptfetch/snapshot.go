package ptfetch

// Snapshot 用户信息快照（JSON 序列化形态，server 侧存 pt_sites.data_json /
// 任务回传体）。-1 = 解析不到（前端显示 '-'），区别于真实 0；nil 指针字段 =
// 未知。store.PtSnapshot 是本类型的别名（历史 JSON 兼容）。
type Snapshot struct {
	Username   string     `json:"username"`
	Level      string     `json:"level"`
	Uploaded   int64      `json:"uploaded"`
	Downloaded int64      `json:"downloaded"`
	Ratio      float64    `json:"ratio"`          // 0 = 未知；-1 = 无限（Inf/∞）
	Seeding    int64      `json:"seeding"`        // -1 = 未知
	SeedingSz  int64      `json:"seeding_sz"`     // Seeding>=0 时有效
	Bonus      float64    `json:"bonus"`          // 主货币（魔力值/积分/爆米花…）
	Bonus2     float64    `json:"bonus2"`         // 次货币（做种积分，⚡）
	HasBonus2  bool       `json:"has_bonus2"`     // 站点无做种积分项时 false
	BonusPH    *float64   `json:"bonus_ph"`       // 时魔值（每小时魔力），nil = 未知
	JoinedAt   int64      `json:"joined_at"`      // 入站时间（秒级），0 = 未知
	Dian       *DianExtra `json:"dian,omitempty"` // 癫影扩展段（连签/今日已签/VIP），其余站点 nil
	Hdh        *HdhExtra  `json:"hdh,omitempty"`  // HDHaven 扩展段（今日已签/VIP），其余站点 nil
	Fc         *FcExtra   `json:"fc,omitempty"`   // 蜂巢扩展段（今日已签/连签/得分/VIP），其余站点 nil
}

// Site 抓取入参（agent 与 server 共用的最小集合——agent 不连库，凭据由任务
// 下发只过内存）。
type Site struct {
	SiteKey string
	BaseURL string
	Cookie  string
	UA      string
	// SigninMode 签到模式（dian115/hdhaven 消费）：空=仅查询不签到，
	// normal/lucky=抓取时自动签到（lucky=dian115 运气签 / hdhaven 赌狗签）；
	// 中转任务由下发侧从站点行捎带。
	SigninMode string
}
