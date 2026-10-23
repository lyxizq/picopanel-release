// Package bilibili B 站开放接口的最小客户端（B 站追更用）：up 主搜索、
// 合集/视频列表解析、音频流下载。口径对齐 web 端：buvid3 Cookie（finger/spi
// 现取）+ WBI 签名（nav 现取密钥）+ Chrome UA + Referer，全部可无登录调用；
// 可选携带服务端配置的 SESSDATA 提升音质档位。客户端只由 agent 侧使用
// （追更抓取全程跑在 agent 机器上，server 不触 B 站）。
package bilibili

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client B 站接口客户端。base 可注入改指 httptest（单测用）。
type Client struct {
	HTTP   *http.Client // API 调用用（短超时）
	DL     *http.Client // CDN 下载用（长超时）
	Cookie string       // 额外 Cookie（SESSDATA，可空）
	base   string

	mu       sync.Mutex
	buvid3   string
	buvid4   string
	buvidAt  time.Time
	imgKey   string
	subKey   string
	wbiAt    time.Time
	wbiFaild bool       // 最近一次 nav 拉取失败（记日志防刷屏用）
	lastReq  time.Time  // 上次 API 请求时刻（节流用）
	throttle time.Duration // 相邻 API 请求最小间隔（用户指定注意风控；0=缺省 1s）
}

// throttleWait 相邻 B 站 API 请求的最小间隔（用户指定：连续请求注意风控）。
// 客户端级全局限速：翻页/搜人/清单/验证/元数据全部经 doGet/rawGet 入口统一
// 生效；CDN 音频下载（fetchToFile，bilivideo 域）不算 B 站 API 不节流。
const defaultThrottle = time.Second

// throttle 距上次 API 请求不足间隔时睡足差值（互斥记账防并发穿透）。
func (c *Client) throttleWait() {
	c.mu.Lock()
	interval := c.throttle
	if interval <= 0 {
		interval = defaultThrottle
	}
	last := c.lastReq
	wait := interval - time.Since(last)
	if wait > 0 {
		c.lastReq = last.Add(interval)
		c.mu.Unlock()
		time.Sleep(wait)
		return
	}
	c.lastReq = time.Now()
	c.mu.Unlock()
}

// apiTimeout / dlTimeout API 与下载两类超时：API 秒级；音频几 MB 但慢链路
// 可能拖到分钟级。
const (
	apiTimeout = 20 * time.Second
	dlTimeout  = 15 * time.Minute
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// NewClient 建客户端。cookie 为追加的 Cookie 串（如 "SESSDATA=xxx"），可空。
func NewClient(cookie string) *Client {
	return &Client{
		HTTP:   &http.Client{Timeout: apiTimeout},
		DL:     &http.Client{Timeout: dlTimeout},
		Cookie: strings.TrimSpace(cookie),
		base:   "https://api.bilibili.com",
	}
}

// toValues map → url.Values（空值跳过，防参与 WBI 签名串）。
func toValues(m map[string]string) url.Values {
	v := url.Values{}
	for k, val := range m {
		if val != "" {
			v.Set(k, val)
		}
	}
	return v
}

// envelope B 站统一响应壳：code 0 为成功，其余为业务错误。
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// apiErr 带 code 的接口错误（-412/-352=风控，-404=不存在等）。
type apiErr struct {
	Code int
	Msg  string
}

func (e *apiErr) Error() string {
	switch e.Code {
	case -412, -352:
		return "触发了 B 站风控拦截，请稍后再试（换时段或配置 SESSDATA 可缓解）"
	case -404:
		return "B 站上不存在该内容（可能已删除或设为私密）"
	case 62002:
		return "稿件不可见（合集内含仅充电/私密视频）"
	}
	if e.Msg != "" {
		return fmt.Sprintf("B 站接口错误（code %d）：%s", e.Code, e.Msg)
	}
	return fmt.Sprintf("B 站接口错误（code %d）", e.Code)
}

// getJSON 发一次带浏览器头的 GET，解析统一壳；风控时刷新 buvid/WBI 重试一次
// （密钥陈旧是被拦的常见原因，与站点零重试纪律不冲突——只补凭据不重打业务）。
func (c *Client) getJSON(path string, params url.Values) (json.RawMessage, error) {
	res, err := c.doGet(path, params, false)
	if err != nil {
		var ae *apiErr
		if errors.As(err, &ae) && (ae.Code == -412 || ae.Code == -352) {
			c.mu.Lock()
			c.buvidAt, c.wbiAt = time.Time{}, time.Time{} // 强制下次现取
			c.mu.Unlock()
			res, err = c.doGet(path, params, false)
		}
	}
	return res, err
}

// getWBIJSON 同 getJSON，但自动附 wts/w_rid 签名（需要 WBI 的接口用）。
func (c *Client) getWBIJSON(path string, params url.Values) (json.RawMessage, error) {
	res, err := c.doGet(path, params, true)
	if err != nil {
		var ae *apiErr
		if errors.As(err, &ae) && (ae.Code == -412 || ae.Code == -352) {
			c.mu.Lock()
			c.buvidAt, c.wbiAt = time.Time{}, time.Time{}
			c.mu.Unlock()
			res, err = c.doGet(path, params, true)
		}
	}
	return res, err
}

// doGet 实际请求：补 buvid Cookie 与（可选）WBI 签名。
func (c *Client) doGet(path string, params url.Values, signed bool) (json.RawMessage, error) {
	c.throttleWait()
	if params == nil {
		params = url.Values{}
	}
	cookie := c.cookieHeader()
	if signed {
		img, sub, err := c.wbiKeys()
		if err != nil {
			return nil, err
		}
		signWBIParams(params, img, sub, time.Now().Unix())
	}
	u := c.base + path
	if enc := params.Encode(); enc != "" {
		u += "?" + enc
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w", err)
	}
	if env.Code != 0 {
		return nil, &apiErr{Code: env.Code, Msg: env.Message}
	}
	return env.Data, nil
}

// cookieHeader 组 Cookie：buvid3/buvid4（现取缓存）+ 用户凭据（SESSDATA）。
func (c *Client) cookieHeader() string {
	b3, b4 := c.buvid()
	parts := make([]string, 0, 3)
	if b3 != "" {
		parts = append(parts, "buvid3="+b3, "buvid4="+b4)
	}
	if c.Cookie != "" {
		parts = append(parts, c.Cookie)
	}
	return strings.Join(parts, "; ")
}

// rotateBuvid 强制下次重取设备指纹（feed/space 未登录抖动重试用：实测同一
// 指纹连续重试大概率连败，换指纹可恢复）。
func (c *Client) rotateBuvid() {
	c.mu.Lock()
	c.buvid3, c.buvid4, c.buvidAt = "", "", time.Time{}
	c.mu.Unlock()
}

// buvid 取设备指纹（finger/spi 无需登录），缓存 24h。
func (c *Client) buvid() (string, string) {
	c.mu.Lock()
	if c.buvid3 != "" && time.Since(c.buvidAt) < 24*time.Hour {
		defer c.mu.Unlock()
		return c.buvid3, c.buvid4
	}
	c.mu.Unlock()
	var data struct {
		B3 string `json:"b_3"`
		B4 string `json:"b_4"`
	}
	if err := json.Unmarshal(c.rawGet("/x/frontend/finger/spi"), &data); err == nil && data.B3 != "" {
		c.mu.Lock()
		c.buvid3, c.buvid4, c.buvidAt = data.B3, data.B4, time.Now()
		c.mu.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buvid3, c.buvid4
}

// wbiKeys 取 WBI 签名密钥（nav 未登录也返回 wbi_img），缓存 1h。
func (c *Client) wbiKeys() (string, string, error) {
	c.mu.Lock()
	if c.imgKey != "" && c.subKey != "" && time.Since(c.wbiAt) < time.Hour {
		img, sub := c.imgKey, c.subKey
		c.mu.Unlock()
		return img, sub, nil
	}
	c.mu.Unlock()
	var data struct {
		WBIImg struct {
			ImgURL string `json:"img_url"`
			SubURL string `json:"sub_url"`
		} `json:"wbi_img"`
	}
	raw := c.rawGet("/x/web-interface/nav")
	if err := json.Unmarshal(raw, &data); err != nil || data.WBIImg.ImgURL == "" {
		c.mu.Lock()
		c.wbiFaild = true
		c.mu.Unlock()
		return "", "", errors.New("获取 WBI 密钥失败（B 站接口不可达）")
	}
	img := wbiKeyFromURL(data.WBIImg.ImgURL)
	sub := wbiKeyFromURL(data.WBIImg.SubURL)
	c.mu.Lock()
	c.imgKey, c.subKey, c.wbiAt = img, sub, time.Now()
	c.mu.Unlock()
	return img, sub, nil
}

// rawGet 裸 GET（无签名、无 buvid 依赖）返回 Data 原文；非 0 壳返回原始 Data
//（nav 未登录 code=-101 但 data.wbi_img 有效，交由调用方判字段）。
func (c *Client) rawGet(path string) json.RawMessage {
	c.throttleWait()
	u := c.base + path
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil
	}
	var env envelope
	if json.Unmarshal(body, &env) != nil {
		return nil
	}
	return env.Data
}

// wbiKeyFromURL 从图片地址取文件名（不含扩展名）作密钥段。
func wbiKeyFromURL(u string) string {
	u = strings.TrimSpace(u)
	if i := strings.LastIndex(u, "/"); i >= 0 {
		u = u[i+1:]
	}
	if i := strings.Index(u, "."); i >= 0 {
		u = u[:i]
	}
	return u
}
