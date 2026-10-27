package ptfetch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// 蜂巢（fengchao.chat）专用链路（457 接入 / 465 蜂蜜余额诉求转 Cookie，2026-10-07）。
// Next.js 自研论坛：浏览器会话接口（Cookie same-origin，无 CSRF 令牌，纯 fetch），
// 凭据=浏览器 F12 整串 Cookie 粘贴：
//
//	GET  /api/auth/me    data.user（账号：username/displayName/levelName/vipLevel/
//	                     points…，字段与 MP 集成 account 同源）+ data.surface（会话
//	                     面：balance 蜂蜜余额/checkedInToday/todayReward 等）
//	POST /api/check-in   {"action":"check-in"}，data.alreadyCheckedIn/reward，
//	                     message 文案
//
// 选 Cookie 的原因：MP 集成 API（Bearer Key）三端点都不暴露蜂蜜余额（points 是
// legacy 论坛积分，余额外部管理后恒 0，真站探针实证）；会话面 surface 才带。
// 行为红线同模块：只 GET 浏览器正常访问的接口 + POST 浏览器签到按钮同一动作，
// 绝不模拟登录（登录走账号密码+人机验证，Cookie 失效只能提示重贴）。

const (
	fcPathSession = "/api/auth/me"
	fcPathCheckin = "/api/check-in"
)

// FcExtra 蜂巢扩展快照段（挂在 Snapshot.Fc，其余站点 nil）。
type FcExtra struct {
	SignedToday bool    `json:"signed_today"` // 今日已签（surface.checkedInToday，站点口径只读可判）
	LastCheckin string  `json:"last_checkin"` // 最后签到日期（YYYY-MM-DD，460 卡片判定用）
	Award       float64 `json:"award"`        // 今日签到得分（网页手动签也有）
	Streak      int64   `json:"streak"`       // 连续签到天数（0=站点未给）
	VIP         bool    `json:"vip"`          // vipLevel > 0
}

// fcUser 会话 user 对象（只取展示需要的字段；points 是 legacy 论坛积分，
// 蜂蜜余额在外部管理下不在它里面；会话 user 无 joinedAt——入站日期在签到
// 信息接口的 registeredOn）。
type fcUser struct {
	Username    string `json:"username"`
	Nickname    string `json:"nickname"`
	DisplayName string `json:"displayName"`
	LevelName   string `json:"levelName"`
	VipLevel    int64  `json:"vipLevel"`
	Points      any    `json:"points"`
}

// fcSurface 会话面（真站实测 2026-10-07）：balance 是对象
// {source:"credit",status:"available",amount:344}，连签叫 currentCheckInStreak，
// 无 todayReward/lastCheckInDate（当日得分只在 POST check-in 响应里）。
type fcSurface struct {
	Balance              any   `json:"balance"`
	CheckedInToday       bool  `json:"checkedInToday"`
	CurrentCheckInStreak int64 `json:"currentCheckInStreak"`
	MaxCheckInStreak     int64 `json:"maxCheckInStreak"`
}

// fcCheckinInfo GET /api/check-in 签到面板信息（只读）：注册日期/连签/货币名。
type fcCheckinInfo struct {
	PointName     string `json:"pointName"`
	RegisteredOn  string `json:"registeredOn"`
	CurrentStreak int64  `json:"currentStreak"`
	MaxStreak     int64  `json:"maxStreak"`
	CheckinReward any    `json:"checkInReward"`
}

// fcNum 宽容数值（整数/浮点/字符串形态都见过）。
func fcNum(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		return parseNumStr(t)
	}
	return 0
}

// fcBalance 余额取数：对象形态 {amount:N}（balanceManagedExternally 的 credit
// 快照）→ 数字/字符串形态（老账号）。
func fcBalance(v any) float64 {
	switch t := v.(type) {
	case map[string]any:
		return fcNum(t["amount"])
	default:
		return fcNum(v)
	}
}

// fcBonus 积分取值：surface.balance（蜂蜜余额，权威）→ user 侧候选（老账号
// points 即余额）→ 0。
func fcBonus(sf *fcSurface, u *fcUser) float64 {
	if n := fcBalance(sf.Balance); n > 0 {
		return n
	}
	if n := fcNum(u.Points); n > 0 {
		return n
	}
	return 0
}

// fcReq 会话请求（Cookie 回放 + JSON Accept；POST 带 JSON 体）。
func fcReq(method, endpoint, cookie, ua string, body []byte) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, endpoint, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", endpoint)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	resp, err := sameHostClient().Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("读取响应失败: %w", err)
	}
	return resp.StatusCode, b, nil
}

// fcData 解信封取 data 对象（{data:{…}} 直取；无 data 壳时按顶层对象用）。
func fcData(b []byte) (json.RawMessage, error) {
	var out struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("响应不是 JSON: %.80s", string(b))
	}
	if len(out.Data) > 0 {
		return out.Data, nil
	}
	return nil, fmt.Errorf("响应缺 data 对象: %.80s", string(b))
}

// fcLoginWall 会话失效判定：401/403，或 data 里无 user。
func fcLoginWall(st int, u *fcUser) error {
	if st == http.StatusUnauthorized || st == http.StatusForbidden {
		return errors.New("登录态已失效（Cookie 过期或已退出登录），请重新粘贴 Cookie")
	}
	if u == nil {
		return errors.New("未登录（Cookie 失效或缺失），请重新粘贴浏览器 Cookie")
	}
	return nil
}

// fetchFengchao 拉快照；SigninMode 配置了 normal 时顺带签到（每日 06:00 轮与
// 手动刷新共用；已签日重复 POST 无害，站点回 alreadyCheckedIn）。
func fetchFengchao(s Site) (*Snapshot, error) {
	snap, _, err := fcRun(s, false)
	return snap, err
}

// SigninFengchao 手动签到（强制执行，空配置回落 normal），返回签到结果文案。
func SigninFengchao(s Site) (*Snapshot, string, error) {
	return fcRun(s, true)
}

func fcRun(s Site, forceSignin bool) (*Snapshot, string, error) {
	mode := s.SigninMode
	if forceSignin && mode != "normal" {
		mode = "normal"
	}
	autoSign := !forceSignin && mode == "normal"

	base := defBaseURL(s)
	if base == "" {
		return nil, "", fmt.Errorf("缺少站点地址（base_url 为空且站点不在目录内）")
	}
	cookie := CleanCookie(s.Cookie)
	if cookie == "" {
		return nil, "", errors.New("未配置 Cookie（浏览器 F12 → 网络 → 任一请求的 Cookie 整串粘贴）")
	}
	ua := s.UA
	if ua == "" {
		ua = DefaultUA
	}

	// 签到先行（浏览器签到按钮同款动作，幂等），之后 /api/auth/me 取到账后余额
	msg := ""
	awardNow := float64(0) // 面板签到那次的得分（网页手动签拿不到，0 不显）
	if autoSign || forceSignin {
		st, b, err := fcReq(http.MethodPost, joinURL(base, fcPathCheckin), cookie, ua, []byte(`{"action":"check-in"}`))
		if err != nil {
			return nil, "", fmt.Errorf("签到: %w", err)
		}
		if st == http.StatusUnauthorized || st == http.StatusForbidden {
			return nil, "", errors.New("登录态已失效（Cookie 过期或已退出登录），请重新粘贴 Cookie")
		}
		// 响应壳 {message, data:{alreadyCheckedIn,…}}（message 是站方文案，
		// 前端直接 toast）——从原始体解双层
		var body struct {
			Message string `json:"message"`
			Data    struct {
				AlreadyCheckedIn bool   `json:"alreadyCheckedIn"`
				Reward           any    `json:"reward"`
				CurrentStreak    int64  `json:"currentStreak"`
				Date             string `json:"date"`
			} `json:"data"`
		}
		_ = json.Unmarshal(b, &body)
		reward := fcNum(body.Data.Reward)
		if !body.Data.AlreadyCheckedIn {
			awardNow = reward
		}
		switch {
		case body.Data.AlreadyCheckedIn:
			msg = "今日已签到"
		case reward > 0:
			msg = fmt.Sprintf("签到成功 +%d 积分", int64(reward))
		case body.Message != "":
			msg = body.Message
		default:
			msg = "签到完成"
		}
		time.Sleep(nexusStepGap)
	}

	// ①会话 me：user（账号）+ surface（余额/已签）
	st, b, err := fcReq(http.MethodGet, joinURL(base, fcPathSession), cookie, ua, nil)
	if err != nil {
		return nil, "", fmt.Errorf("获取用户信息: %w", err)
	}
	data, derr := fcData(b)
	var sess struct {
		User    *fcUser    `json:"user"`
		Surface *fcSurface `json:"surface"`
	}
	if derr == nil {
		_ = json.Unmarshal(data, &sess)
	}
	if err := fcLoginWall(st, sess.User); err != nil {
		return nil, "", err
	}
	u, sf := sess.User, sess.Surface
	if sf == nil {
		sf = &fcSurface{}
	}

	// ②签到面板信息（只读）：注册日期/连签权威源；失败不致命（连签退 surface）
	ci := &fcCheckinInfo{}
	if st2, b2, err2 := fcReq(http.MethodGet, joinURL(base, fcPathCheckin), cookie, ua, nil); err2 == nil && st2 == http.StatusOK {
		if data2, err3 := fcData(b2); err3 == nil {
			_ = json.Unmarshal(data2, ci)
		}
	}

	award := awardNow
	signed := sf.CheckedInToday
	snap := &Snapshot{
		Username: u.DisplayName,
		Level:    u.LevelName,
		Bonus:    fcBonus(sf, u),
		Fc: &FcExtra{
			SignedToday: signed,
			LastCheckin: lastCheckin(signed),
			Award:       award,
			Streak:      firstPositive(ci.CurrentStreak, sf.CurrentCheckInStreak),
			VIP:         u.VipLevel > 0,
		},
	}
	switch {
	case snap.Username == "" && u.Nickname != "":
		snap.Username = u.Nickname
	case snap.Username == "":
		snap.Username = u.Username
	}
	if t := parseTimeStr(ci.RegisteredOn); t > 0 {
		snap.JoinedAt = t
	}
	return snap, msg, nil
}

// lastCheckin 最后签到日期：已签=今天（站点未回传明细日期，460 判定
// last==今天由此成立；未签留空回落 signed_today 布尔）。
func lastCheckin(signed bool) string {
	if !signed {
		return ""
	}
	return time.Now().Format("2006-01-02")
}

// firstPositive 首个正数（连签权威源优先，surface 兜底）。
func firstPositive(vs ...int64) int64 {
	for _, v := range vs {
		if v > 0 {
			return v
		}
	}
	return 0
}
