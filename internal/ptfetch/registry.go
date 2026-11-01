package ptfetch

import "fmt"

// 内置站点目录（391）：对接用户点名的站点，域名/口径参考
// PT-depiler（github.com/pt-plugins/PT-depiler）站点定义。kind 决定抓取链路：
// nexusphp=通用 NexusPHP 页面解析（含 TTG 等变体，标签集已兼容），
// mteam=M-Team 官方 API（x-api-key），
// dian115=癫影自研网关（portal_token + 浏览器证明/请求签名，见 dian115.go），
// hdhaven=栖影 Next.js 站 JSON API（hdh_session Cookie，见 hdhaven.go）。

// SiteDef 目录条目。
type SiteDef struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	URL  string `json:"url"`
	Kind string `json:"kind"` // nexusphp / mteam
	Auth string `json:"auth"` // cookie / apikey
}

// Catalog 内置站点目录（次序即添加下拉的次序）。
func Catalog() []SiteDef {
	return []SiteDef{
		{Key: "audiences", Name: "Audiences", URL: "https://audiences.me", Kind: "nexusphp", Auth: "cookie"},
		{Key: "btschool", Name: "BTSCHOOL", URL: "https://pt.btschool.club", Kind: "nexusphp", Auth: "cookie"},
		{Key: "carpt", Name: "CarPT", URL: "https://carpt.net", Kind: "nexusphp", Auth: "cookie"},
		{Key: "chdbits", Name: "CHDBits", URL: "https://ptchdbits.co", Kind: "nexusphp", Auth: "cookie"},
		{Key: "dian115", Name: "癫影", URL: "https://m.dian115.com", Kind: "dian115", Auth: "cookie"},
		{Key: "discfan", Name: "DiscFan", URL: "https://discfan.net", Kind: "nexusphp", Auth: "cookie"},
		{Key: "fengchao", Name: "蜂巢", URL: "https://fengchao.chat", Kind: "fengchao", Auth: "cookie"},
		{Key: "hddolby", Name: "HD Dolby", URL: "https://www.hddolby.com", Kind: "nexusphp", Auth: "cookie"},
		{Key: "hdhaven", Name: "栖影 HDHaven", URL: "https://hdhaven.com", Kind: "hdhaven", Auth: "cookie"},
		{Key: "hdhome", Name: "HDHome", URL: "https://hdhome.org", Kind: "nexusphp", Auth: "cookie"},
		{Key: "hdsky", Name: "HDSky", URL: "https://hdsky.me", Kind: "nexusphp", Auth: "cookie"},
		{Key: "hdtime", Name: "HDTime", URL: "https://hdtime.org", Kind: "nexusphp", Auth: "cookie"},
		{Key: "hhanclub", Name: "憨憨", URL: "https://hhanclub.net", Kind: "nexusphp", Auth: "cookie"},
		{Key: "keepfrds", Name: "KEEPFRDS", URL: "https://pt.keepfrds.com", Kind: "nexusphp", Auth: "cookie"},
		{Key: "longpt", Name: "LongPT", URL: "https://longpt.org", Kind: "nexusphp", Auth: "cookie"},
		{Key: "mteam", Name: "M-Team", URL: "https://kp.m-team.cc", Kind: "mteam", Auth: "apikey"},
		{Key: "nicept", Name: "NicePT", URL: "https://www.nicept.net", Kind: "nexusphp", Auth: "cookie"},
		{Key: "opencd", Name: "OpenCD", URL: "https://open.cd", Kind: "nexusphp", Auth: "cookie"},
		{Key: "ourbits", Name: "OurBits", URL: "https://ourbits.club", Kind: "nexusphp", Auth: "cookie"},
		{Key: "piggo", Name: "Piggo", URL: "https://piggo.me", Kind: "nexusphp", Auth: "cookie"},
		{Key: "pter", Name: "PTer", URL: "https://pterclub.net", Kind: "nexusphp", Auth: "cookie"},
		{Key: "pthome", Name: "PTHome", URL: "https://pthome.net", Kind: "nexusphp", Auth: "cookie"},
		{Key: "ptsbao", Name: "PTSBao", URL: "https://ptsbao.club", Kind: "nexusphp", Auth: "cookie"},
		{Key: "pttime", Name: "PTTime", URL: "https://www.pttime.org", Kind: "nexusphp", Auth: "cookie"},
		{Key: "soulvoice", Name: "SoulVoice", URL: "https://pt.soulvoice.club", Kind: "nexusphp", Auth: "cookie"},
		{Key: "springsunday", Name: "SpringSunday", URL: "https://springsunday.net", Kind: "nexusphp", Auth: "cookie"},
		{Key: "tjupt", Name: "北洋园PT", URL: "https://tjupt.org", Kind: "nexusphp", Auth: "cookie"},
		{Key: "totheglory", Name: "ToTheGlory", URL: "https://totheglory.im", Kind: "nexusphp", Auth: "cookie"},
		{Key: "ubits", Name: "UBits", URL: "https://ubits.club", Kind: "nexusphp", Auth: "cookie"},
	}
}

// SiteDefByKey 目录键 → 条目（不在目录返回 nil）。
func SiteDefByKey(key string) *SiteDef {
	for _, d := range Catalog() {
		if d.Key == key {
			return &d
		}
	}
	return nil
}

// FetchSite 按站点类型分发抓取（不写库）。server 侧 Manager 与 agent 中转执行
// 共用；cookie 由调用方传入（server 从库解密，agent 从任务下发）。
func FetchSite(s Site) (*Snapshot, error) {
	def := SiteDefByKey(s.SiteKey)
	if def == nil {
		return nil, fmt.Errorf("未知站点 %q", s.SiteKey)
	}
	base := normalizeBaseURL(s.BaseURL)
	if base == "" {
		base = def.URL
	}
	ua := s.UA
	if ua == "" {
		ua = DefaultUA
	}
	if def.Kind == "mteam" {
		return fetchMTeam(base, s.Cookie, ua)
	}
	if def.Kind == "dian115" {
		return fetchDian115(s)
	}
	if def.Kind == "hdhaven" {
		return fetchHDHaven(s)
	}
	if def.Kind == "fengchao" {
		return fetchFengchao(s)
	}
	return fetchNexus(base, s.Cookie, ua, s.SiteKey)
}

// defBaseURL 站点基址共用口径：配置优先，空回落目录默认域（dian115/hdhaven 各有
// 同款私有 helper，新增 kind 直接用这个）。
func defBaseURL(s Site) string {
	if b := normalizeBaseURL(s.BaseURL); b != "" {
		return b
	}
	if def := SiteDefByKey(s.SiteKey); def != nil && def.URL != "" {
		return normalizeBaseURL(def.URL)
	}
	return ""
}
