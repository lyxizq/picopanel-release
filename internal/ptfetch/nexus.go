package ptfetch

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// NexusPHP 通用解析（391，口径参考 PT-depiler 的 NexusPHP schema）：
// ①GET /index.php → 用户 id/用户名（info_block 的 userdetails 链接）；
// ②GET /userdetails.php?id=N → 上传/下载/分享率/等级/魔力（主货币）/做种积分
//   （次货币）/加入日期；③GET /getusertorrentlistajax.php?userid=N&type=seeding
//   → 做种数/做种量（三种页面形态都兼容，TTG 退回详情页做种表）；
// ④GET /mybonus.php → 时魔值。TTG 等变体的行标签差异（上传量/积分/注册日期）
// 并入各标签集。⑤keepfrds 新版站点的魔力值权威源是 /api/userdetails.php
// JSON 接口（PT-depiler 同款口径），网页行解析不可靠，末尾以接口值覆盖。

var (
	reUploaded   = regexp.MustCompile(`(?:上[传傳]量|Uploaded)[^\d]*([\d.,]+ ?[KMGTPE]?i?B)`)
	reDownloaded = regexp.MustCompile(`(?:下[载載]量|Downloaded)[^\d]*([\d.,]+ ?[KMGTPE]?i?B)`)
	// 无穷分享率：inf/∞/--- 之外还有中文「无限」（longpt 等站传输行/信息条
	// 用它表示 0 下载量的除零形态，496）。
	reInfRatio   = regexp.MustCompile(`(?i)(?:分享率|分享比率|Ratio)[：:\s]*(inf\.?|∞|无限|無限|--+)`)
	// 等级 class 形态（484 憨憨/audiences）：NexusPHP 把等级名（空格转下划线）
	// 挂「_Name」后缀缀在元素 class 上，如 Elite_User_Name。
	reLevelClass = regexp.MustCompile(`(.+)_Name`)
)

// stripLevelAlias 剥「(别名)正文」/「（别名）正文」前缀括号（487，audiences
// 新版 b 文本双语形态）；剥后正文为空视为纯括号名，原样保留。
func stripLevelAlias(s string) string {
	s = strings.TrimSpace(s)
	for _, p := range [][2]string{{"(", ")"}, {"（", "）"}} {
		if strings.HasPrefix(s, p[0]) {
			if i := strings.Index(s, p[1]); i >= 0 {
				if rest := strings.TrimSpace(s[i+len(p[1]):]); rest != "" {
					return rest
				}
			}
			break
		}
	}
	return s
}

// ratioOf 从文本解析分享率；Inf/∞/--- 记 -1（前端显示 ∞），解析失败 0。
func ratioOf(text string) float64 {
	if reInfRatio.MatchString(text) {
		return -1
	}
	for _, k := range []string{"分享率", "分享比率", "Ratio"} {
		if i := strings.Index(text, k); i >= 0 {
			if v := parseNumStr(text[i+len(k):]); v > 0 {
				return v
			}
		}
	}
	return 0
}

// nexusStepGap 同一站连续请求之间的间隔（体面抓取）。
const nexusStepGap = 400 * time.Millisecond

// nexusSess 一次抓取内的会话 Cookie 延续：站点随响应重发的 Set-Cookie
// （NexusPHP 防刷新轮换 server_name_session 等）吸收进来，后续步骤带上
// 新值——静态配置 Cookie 若已被站点轮换，不带新值每步都被当无会话访问。
type nexusSess struct {
	m map[string]string
}

func (s *nexusSess) absorb(cs []*http.Cookie) {
	if len(cs) == 0 {
		return
	}
	if s.m == nil {
		s.m = map[string]string{}
	}
	for _, c := range cs {
		if c.Name != "" {
			s.m[c.Name] = c.Value
		}
	}
}

// header 会话 Cookie（新值覆盖用户配置里的同名旧值）并到基础 Cookie 串。
func (s *nexusSess) header(base string) string {
	if len(s.m) == 0 {
		return base
	}
	vals := map[string]string{}
	var order []string
	for _, p := range strings.Split(base, ";") {
		p = strings.TrimSpace(p)
		if i := strings.Index(p, "="); i > 0 {
			name := p[:i]
			if _, seen := vals[name]; !seen {
				order = append(order, name)
			}
			vals[name] = p[i+1:]
		}
	}
	for k, v := range s.m {
		if _, seen := vals[k]; !seen {
			order = append(order, k)
		}
		vals[k] = v
	}
	var b strings.Builder
	for i, name := range order {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(name)
		b.WriteString("=")
		b.WriteString(vals[name])
	}
	return b.String()
}

// fetch 会话内拉页（自动并吸收下发 Cookie）。
func (s *nexusSess) fetch(endpoint, baseCookie, ua string) (string, error) {
	body, cs, err := fetchPageCapture(endpoint, s.header(baseCookie), ua)
	if err == nil {
		s.absorb(cs)
	}
	return body, err
}

// looksLikeUserDetails 详情页体检：须含加入/注册日期或传输行标记。注意
// 「上传量/下载量/分享率」不能作判据——longpt 等站的首页信息条同样有
// 这三项（2026-10-08 实抓验证：首页仅缺加入日期/传输标记），详情页形态
// 再怎么改版（audiences 行头让位 userbar、憨憨 span 行）加入日期文本
// 都在。都没有说明拿到的是别的页面（防刷新跳转落首页、挑战页等），
// 解析只会产出「上传量碰巧命中、其余全零」的垃圾快照，报错暴露（496）。
func looksLikeUserDetails(docText string) bool {
	for _, k := range []string{"加入日期", "加入時間", "注册日期", "註冊日期", "注册时间", "加入时间", "Join date", "Join Date", "传输", "傳送", "Transfers"} {
		if strings.Contains(docText, k) {
			return true
		}
	}
	return false
}

// fetchNexus 抓一个 NexusPHP 站点（含 TTG 变体）的用户信息。siteKey 用于
// 站点特例分支（keepfrds 新版 API 覆盖魔力值）。
func fetchNexus(base, cookie, ua, siteKey string) (*Snapshot, error) {
	snap := &Snapshot{Seeding: -1}
	var sess nexusSess

	// ①index.php：用户 id + 用户名（未登录站点的 index 也 200，但拿不到链接）
	idxBody, err := sess.fetch(joinURL(base, "/index.php"), cookie, ua)
	if err != nil {
		return nil, fmt.Errorf("首页: %w", err)
	}
	idx := parseHTML(idxBody)
	var userID string
	for _, a := range findLinks(idx, "userdetails.php") {
		var id string
		if u, err := url.Parse(attrOf(a, "href")); err == nil {
			id = u.Query().Get("id")
		}
		name := strings.TrimSpace(textOf(a))
		if userID == "" && id != "" {
			userID = id
		}
		if snap.Username == "" && name != "" {
			snap.Username = name
		}
		if userID != "" && snap.Username != "" {
			break
		}
	}
	if userID == "" {
		return nil, fmt.Errorf("未在首页找到用户信息（Cookie 失效或站点改版）")
	}
	time.Sleep(nexusStepGap)

	// ②userdetails.php?id=N：主体数据。先体检页面形态：拿到的不像详情页
	//（防刷新跳转落首页/挑战页）时，吸收站点本轮下发的会话 Cookie 后重试
	// 一次，仍不对就报错暴露，绝不静默解析错页（496）。
	detBody, err := sess.fetch(joinURL(base, "/userdetails.php?id="+url.QueryEscape(userID)), cookie, ua)
	if err != nil {
		return nil, fmt.Errorf("用户详情: %w", err)
	}
	det := parseHTML(detBody)
	if !looksLikeUserDetails(textOf(det)) {
		time.Sleep(nexusStepGap)
		detBody, err = sess.fetch(joinURL(base, "/userdetails.php?id="+url.QueryEscape(userID)), cookie, ua)
		if err != nil {
			return nil, fmt.Errorf("用户详情: %w", err)
		}
		det = parseHTML(detBody)
		if !looksLikeUserDetails(textOf(det)) {
			return nil, fmt.Errorf("用户详情页内容异常（站点返回了非详情页，疑似防爬跳转），已重试仍失败")
		}
	}
	docText := textOf(det)

	// 标准 NexusPHP 把上传/下载/分享率合并在「传输」行，正则兜底各页面形态
	hay := docText
	if row := findRowExact(det, "传输", "傳送", "Transfers"); row != nil {
		hay = rowValue(row) + "\n" + hay
	}
	if m := reUploaded.FindStringSubmatch(hay); m != nil {
		snap.Uploaded = parseSizeBytes(m[1])
	}
	if m := reDownloaded.FindStringSubmatch(hay); m != nil {
		snap.Downloaded = parseSizeBytes(m[1])
	}

	// 分享率：独立行优先，退回传输行文本，最后自算
	if v := rowValue(findRowExact(det, "分享率", "分享比率", "Ratio")); v != "" {
		snap.Ratio = ratioOf(v)
	}
	if snap.Ratio == 0 {
		if row := findRowExact(det, "传输", "傳送", "Transfers"); row != nil {
			snap.Ratio = ratioOf(rowValue(row))
		}
	}
	if snap.Ratio == 0 && snap.Downloaded > 0 {
		snap.Ratio = math.Round(float64(snap.Uploaded)/float64(snap.Downloaded)*100) / 100
	}

	// 等级（484 扩：链路对齐 456 的 bonus/join——行头容器扩到 td/span/div，
	// exact→contains（pttime 行头带装饰、「Class」太泛不进 contains）→内联；
	// 值形态三看：<img title> 徽章、<b title> 属性（pttime 等级名挂属性，
	// 格内无文本）、纯文本）；憨憨/audiences 新版行头表已无此行，走站特分支
	// 覆盖（PT-depiler 同款口径）。
	lvlRow := findRowAny(det, "等级", "等級", "Class")
	if lvlRow == nil {
		lvlRow = findRowAnyContains(det, "等级", "等級")
	}
	if lvlRow != nil {
		snap.Level = levelTextOf(nextElem(lvlRow))
	}
	if snap.Level == "" {
		snap.Level = labelInline(det, "等级", "等級")
	}
	if siteKey == "hhanclub" {
		// 憨憨新版详情页无等级行：等级挂用户链接的 class（如
		// Elite_User_Name，PT-depiler a[class*='_Name']），下划线转空格。
		for _, a := range findLinks(det, "userdetails.php") {
			if m := reLevelClass.FindStringSubmatch(attrOf(a, "class")); m != nil {
				snap.Level = strings.ReplaceAll(m[1], "_", " ")
				break
			}
		}
	}
	if siteKey == "audiences" {
		// audiences 新版详情页等级在 hero 的 ud-class 里（PT-depiler
		// span.ud-class > b）：b 文本优先，退 b 的 class XXX_Name 形态。
		if el := findByClass(det, "ud-class"); el != nil {
			if lv := classBLevel(el); lv != "" {
				snap.Level = lv
			}
		}
	}
	// 展示串归一（487）：audiences 新版 b 文本是「(头号玩家)Extreme User」
	// 双语别名形态，等级 chip 宽度有限必截断——剥前缀括号别名，与其他站
	// 纯等级名对齐；纯括号名（剥后正文空）原样保留。
	snap.Level = stripLevelAlias(snap.Level)

	// 主货币（积分列）四轮（456 扩：憨憨新版=憨豆 span 行、HD Dolby=鲸币、
	// UBits=U币/UBits Coin；行头容器扩到 td/span/div，再兜同格内联形态）：
	// 站特称 exact → 站特称 contains → 特称内联 → 泛称「积分」必须最后兜
	//——泛称排进前面轮次会按文档序抢在特称前面命中别的行，魔力值取错（433）。
	bonusLabels := []string{"魔力值", "猫粮", "貓糧", "Karma Points", "魅力值", "沙粒", "星焱", "爆米花", "憨豆", "鲸币", "U币", "UBits Coin", "魔力"}
	bonusRow := findRowAny(det, bonusLabels...)
	if bonusRow == nil {
		bonusRow = findRowAnyContains(det, "魔力值", "魅力值", "猫粮", "貓糧", "Karma Points", "憨豆", "鲸币", "U币", "UBits Coin", "爆米花")
	}
	bonusVal := rowValue(bonusRow)
	if bonusVal == "" {
		bonusVal = labelInline(det, bonusLabels...)
	}
	if bonusVal == "" {
		bonusVal = rowValue(findRowExact(det, "积分", "積分"))
	}
	snap.Bonus = parseNumStr(bonusVal)

	// audiences 新版用户栏（456，PT-depiler 口径）：改版后积分在 userbar
	// 紧凑指标里，行头表已让位；类名直取，取到即覆盖（失败静默沿用上值）。
	if siteKey == "audiences" {
		if el := findByClass(det, "site-userbar__compact-metric--bonus"); el != nil {
			if v := parseNumStr(textOf(el)); v > 0 {
				snap.Bonus = v
			}
		}
	}

	// 次货币（做种积分，⚡）
	if row := findRowExact(det, "做种积分", "做種積分", "Seeding Points", "保种积分", "保種積分"); row != nil {
		snap.Bonus2 = parseNumStr(rowValue(row))
		snap.HasBonus2 = true
	}

	// 入站时间（456 扩：行头容器扩到 td/span/div，contains 兜带前后缀/双语
	// 行头，内联兜「加入日期：值」同格形态；值容忍「2022-09-22 (xxx前)」尾巴）
	joinLabels := []string{"加入日期", "加入時間", "注册日期", "註冊日期", "注册时间", "註冊時間", "加入时间", "Join date", "Join Date"}
	joined := rowValue(findRowAny(det, joinLabels...))
	if joined == "" {
		joined = rowValue(findRowAnyContains(det, joinLabels...))
	}
	if joined == "" {
		joined = labelInline(det, joinLabels...)
	}
	snap.JoinedAt = parseTimeStr(joined)

	time.Sleep(nexusStepGap)

	// ③做种数/做种量（拿不到置 -1，前端显示 '-'；TTG 退回详情页做种表）
	snap.Seeding, snap.SeedingSz = fetchSeeding(base, sess.header(cookie), ua, userID)
	if snap.Seeding < 0 {
		snap.Seeding, snap.SeedingSz = seedingFromDoc(findById(det, "ka2"))
	}

	time.Sleep(nexusStepGap)

	// ④时魔值（mybonus.php；无此页/解析不到置 nil）
	if bp, ok := fetchBonusPerHour(base, sess.header(cookie), ua); ok {
		snap.BonusPH = &bp
	}

	// ⑤keepfrds 新版主数据走 /api/userdetails.php JSON，user.bonus 是权威
	// 魔力值，取到即覆盖网页解析结果；接口失败静默沿用网页值。
	if siteKey == "keepfrds" {
		time.Sleep(nexusStepGap)
		if v, ok := fetchKeepfrdsBonus(base, sess.header(cookie), ua); ok {
			snap.Bonus = v
		}
	}
	return snap, nil
}

// fetchKeepfrdsBonus keepfrds 新版 /api/userdetails.php 的 user.bonus（数字，
// 宽容容忍字符串形态）。接口不通/结构不对/非数字字符串返回 false。
func fetchKeepfrdsBonus(base, cookie, ua string) (float64, bool) {
	body, err := fetchPage(joinURL(base, "/api/userdetails.php"), cookie, ua)
	if err != nil {
		return 0, false
	}
	var resp struct {
		User struct {
			Bonus any `json:"bonus"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return 0, false
	}
	switch v := resp.User.Bonus.(type) {
	case float64:
		return v, true
	case string:
		if n := parseNumStr(v); n > 0 {
			return n, true
		}
	}
	return 0, false
}

// fetchSeeding 做种数/量：getusertorrentlistajax.php 三种页面形态——
// ①标准 NexusPHP 的「N | 大小」摘要行；②Audiences 式 Total 汇总行；
// ③普通表格（数行数、识别大小列求和）。全部失败返回 -1（未知）。
func fetchSeeding(base, cookie, ua, userID string) (int64, int64) {
	uid := url.QueryEscape(userID)
	req, err := newRequest(joinURL(base, "/getusertorrentlistajax.php?userid="+uid+"&type=seeding"), cookie, ua)
	if err != nil {
		return -1, 0
	}
	// Referer 带 userdetails（Audiences 等站没有它不给数据）
	req.Header.Set("Referer", joinURL(base, "/userdetails.php?id="+uid))
	body, err := doRequest(req)
	if err != nil {
		return -1, 0
	}
	if !strings.Contains(body, "<table") && !strings.Contains(body, "<div") {
		return -1, 0
	}
	doc := parseHTML(body)
	if doc == nil {
		return -1, 0
	}
	// ①「N | 大小」摘要行
	for _, div := range eachTag(doc, "div") {
		t := textOf(div)
		if strings.Contains(t, "|") {
			parts := strings.SplitN(t, "|", 2)
			cnt := int64(parseNumStr(parts[0]))
			sz := parseSizeBytes(parts[1])
			if cnt > 0 || sz > 0 {
				return cnt, sz
			}
		}
	}
	tables := eachTag(doc, "table")
	if len(tables) == 0 {
		return -1, 0
	}
	if cnt, sz := seedingFromTable(tables[len(tables)-1]); cnt >= 0 {
		return cnt, sz
	}
	return -1, 0
}

// seedingFromTable 解析做种表格：Total 汇总行优先，退回数行数 + 大小列求和；
// 不是做种表（无数据行）返回 -1。
func seedingFromTable(table *html.Node) (int64, int64) {
	rows := eachTag(table, "tr")
	if len(rows) == 0 {
		return -1, 0
	}
	for _, tr := range rows {
		tds := childElems(tr, "td")
		if len(tds) >= 3 && strings.EqualFold(strings.TrimSpace(textOf(tds[0])), "Total") {
			return int64(parseNumStr(textOf(tds[1]))), parseSizeBytes(textOf(tds[2]))
		}
	}
	if len(rows) <= 1 {
		return -1, 0
	}
	sizeIdx := -1
	var total int64
	for _, tr := range rows[1:] {
		tds := childElems(tr, "td")
		if sizeIdx < 0 {
			for i, td := range tds {
				if sizeRe.MatchString(textOf(td)) {
					sizeIdx = i
					break
				}
			}
		}
		if sizeIdx >= 0 && sizeIdx < len(tds) {
			total += parseSizeBytes(textOf(tds[sizeIdx]))
		}
	}
	return int64(len(rows) - 1), total
}

// seedingFromDoc 详情页做种容器（TTG 的 div#ka2）：套用同一套表格逻辑。
func seedingFromDoc(container *html.Node) (int64, int64) {
	if container == nil {
		return -1, 0
	}
	tables := eachTag(container, "table")
	if len(tables) == 0 {
		return -1, 0
	}
	return seedingFromTable(tables[len(tables)-1])
}

// fetchBonusPerHour 时魔值：mybonus.php 的「你当前每小时能获取 N」或 TTG 的
// 「总计」行。拿不到返回 false（前端显示 '-'）。
func fetchBonusPerHour(base, cookie, ua string) (float64, bool) {
	body, err := fetchPage(joinURL(base, "/mybonus.php"), cookie, ua)
	if err != nil {
		return 0, false
	}
	doc := parseHTML(body)
	if doc == nil {
		return 0, false
	}
	if row := findRowExact(doc, "总计", "總計", "Total"); row != nil {
		fields := strings.Fields(rowValue(row))
		if len(fields) > 0 {
			return parseNumStr(fields[0]), true
		}
	}
	for _, div := range eachTag(doc, "div") {
		t := textOf(div)
		if strings.Contains(t, "每小时能获取") || strings.Contains(t, "每小時能獲取") || strings.Contains(t, "currently getting") {
			if v := parseNumStr(t); v > 0 {
				return v, true
			}
		}
	}
	return 0, false
}
