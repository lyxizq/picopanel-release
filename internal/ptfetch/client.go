package ptfetch

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 抓取链路共用：带 Cookie 的浏览器拟真 HTTP 客户端 + 文本数值解析工具。

// DefaultUA 默认浏览器 UA。Cloudflare 站的 cf_clearance 与 UA 绑定，站点配置
// 了自定义 UA 时用自定义的（前端提示用户一并填浏览器 UA）。
const DefaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

const reqTimeout = 20 * time.Second

// cleanCookie 清洗用户粘贴的 Cookie 串：容忍换行/多余分号与 "Cookie: " 前缀。
func CleanCookie(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "Cookie:")
	raw = strings.TrimPrefix(raw, "cookie:")
	parts := strings.Split(raw, ";")
	var keep []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || !strings.Contains(p, "=") {
			continue
		}
		keep = append(keep, p)
	}
	return strings.Join(keep, "; ")
}

// newRequest 构造带浏览器头的 GET 请求。
func newRequest(endpoint, cookie, ua string) (*http.Request, error) {
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.6")
	// 浏览器导航请求的标准头（防 WAF 按「头不像浏览器」挑刺；刻意不带
	// Sec-Fetch-*：导航与 ajax 形态各异，填错比不填更扎眼）
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	return req, nil
}

// errCrossHostRedirect 站点把请求重定向到别的域名——Go 的 http.Client 跟随
// 重定向时会原样重发包括 Cookie 在内的全部请求头，若站点域名被接管/跳到
// 钓鱼域，等于把用户会话 Cookie 白送给第三方。封号风险之外更是凭据泄露，
// 直接熔断报错，绝不跨域跟。
var errCrossHostRedirect = fmt.Errorf("站点重定向到其他域名，已中止跟随（防止 Cookie 外发到第三方域）")

// mergeSetCookie 把上一跳响应下发的 Set-Cookie 并入下一跳请求的 Cookie 头：
// NexusPHP 防刷新机制靠轮换 server_name_session 这类会话 Cookie（302 挑战
// 或随响应重发），静态 Cookie 头不回收就过不了闭环，会一直被当成无会话
// 访问。同名以后来者覆盖（站点新签的优先于用户配置的旧值）。
func mergeSetCookie(req *http.Request, prev *http.Response) {
	if prev == nil || len(prev.Cookies()) == 0 {
		return
	}
	cur := req.Header.Get("Cookie")
	vals := map[string]string{} // name → value（保序见 idx）
	var order []string
	for _, p := range strings.Split(cur, ";") {
		p = strings.TrimSpace(p)
		if i := strings.Index(p, "="); i > 0 {
			name := p[:i]
			if _, seen := vals[name]; !seen {
				order = append(order, name)
			}
			vals[name] = p[i+1:]
		}
	}
	for _, c := range prev.Cookies() {
		if c.Name == "" {
			continue
		}
		if _, seen := vals[c.Name]; !seen {
			order = append(order, c.Name)
		}
		vals[c.Name] = c.Value
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
	req.Header.Set("Cookie", b.String())
}

// sameHostClient 带超时与跨域重定向熔断的客户端。
func sameHostClient() *http.Client {
	return &http.Client{
		Timeout: reqTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				return errCrossHostRedirect
			}
			if len(via) >= 5 {
				return fmt.Errorf("重定向次数过多")
			}
			mergeSetCookie(req, req.Response)
			return nil
		},
	}
}

// doRequest 执行请求，返回响应体。非 200 报错带状态码与地址。
func doRequest(req *http.Request) (string, error) {
	resp, err := sameHostClient().Do(req)
	if err != nil {
		if errors.Is(err, errCrossHostRedirect) {
			return "", errCrossHostRedirect
		}
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d（%s）", resp.StatusCode, req.URL)
	}
	return string(body), nil
}

// fetchPage 带 Cookie/UA 拉一个页面。
func fetchPage(endpoint, cookie, ua string) (string, error) {
	req, err := newRequest(endpoint, cookie, ua)
	if err != nil {
		return "", err
	}
	return doRequest(req)
}

// fetchPageCapture 同 fetchPage，额外返回最终响应下发的 Cookie（含重定向
// 途中的合并结果，供跨请求会话延续——NexusPHP 防刷新随响应轮换会话
// Cookie，只带静态配置头的话每一步都在「重新认门」）。
func fetchPageCapture(endpoint, cookie, ua string) (string, []*http.Cookie, error) {
	req, err := newRequest(endpoint, cookie, ua)
	if err != nil {
		return "", nil, err
	}
	resp, err := sameHostClient().Do(req)
	if err != nil {
		if errors.Is(err, errCrossHostRedirect) {
			return "", nil, errCrossHostRedirect
		}
		return "", nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("HTTP %d（%s）", resp.StatusCode, req.URL)
	}
	return string(body), resp.Cookies(), nil
}

var (
	sizeRe = regexp.MustCompile(`([\d.,]+)\s*([KMGTPE]?i?B)`)
	numRe  = regexp.MustCompile(`[\d][\d.,]*`)
	dateRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
)

// parseSizeBytes "22.85 TiB"/"502.02 GiB"/"0.00 B" → 字节数（解析失败 0）。
func parseSizeBytes(s string) int64 {
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	if err != nil {
		return 0
	}
	mult := 1.0
	switch strings.TrimRight(m[2], "iB") {
	case "K":
		mult = 1 << 10
	case "M":
		mult = 1 << 20
	case "G":
		mult = 1 << 30
	case "T":
		mult = 1 << 40
	case "P":
		mult = 1 << 50
	case "E":
		mult = 1 << 60
	}
	return int64(v * mult)
}

// parseNumStr "3,729,931.60" / "1.23 万"? → 浮点（取串内首个数字；解析失败 0）。
func parseNumStr(s string) float64 {
	m := numRe.FindString(s)
	if m == "" {
		return 0
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(m, ",", ""), 64)
	if err != nil {
		return 0
	}
	return v
}

// parseTimeStr 常见站点时间文本 → 秒级时间戳（0 = 解析失败）。容忍
// "2022-09-22"、"2022-09-22 10:11:12"、"2022/09/22 10:11"、"2022-09-22 10:11" 后
// 带括注等形态，取首个 yyyy-MM-dd 前缀兜底。
func parseTimeStr(s string) int64 {
	s = strings.TrimSpace(s)
	layouts := []string{
		"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02",
		"2006/01/02 15:04:05", "2006/01/02 15:04", "2006/01/02",
		"2006-01-0215:04:05",
		time.RFC1123, time.RFC822, "02-01-2006", "01-02-2006",
	}
	head := s
	if d := dateRe.FindString(s); d != "" {
		head = d
	}
	for _, l := range layouts {
		for _, t := range []string{s, head} {
			if t == "" {
				continue
			}
			if v, err := time.ParseInLocation(l, t, time.Local); err == nil {
				return v.Unix()
			}
		}
	}
	return 0
}

// joinURL 基站点 + 路径（base 容忍尾斜杠）。
func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}

// normalizeBaseURL 基址规范化：去尾斜杠；无 scheme 补 https://（镜像手填容错）。
func normalizeBaseURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	return strings.TrimRight(u, "/")
}

// hostOf 提取主机名（报错信息带站点用；解析失败原样返回）。
func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Host
	}
	return u
}
