package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestAssistantAPI 个人助理接口：倒数日 CRUD（含农历换算与同名拒绝）、日历聚合
// （倒数日与会员落在对应日期、日标记带农历与节假日）、助理设置回写、ICS 导出令牌
// （无令牌 404 → 生成后 200 且为 text/calendar → 重置后旧令牌 404）、未登录 401。
func TestAssistantAPI(t *testing.T) {
	r, token, _ := newMemRouter(t)
	base := "/api/v1"

	if w := memReq(r, "", "GET", base+"/countdowns", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，得 %d", w.Code)
	}

	// 倒数日：农历每年（2025-10-06=八月十五）
	res := memReq(r, token, "POST", base+"/countdowns", `{"name":"中秋","kind":"yearly","lunar":true,"date":"2025-10-06","color":"#f56c6c"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("新建倒数日应 200: %s", res.Body.String())
	}
	var list struct {
		Items []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			NextDate   string `json:"next_date"`
			LunarLabel string `json:"lunar_label"`
			Days       int    `json:"days"`
		} `json:"items"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("返回清单不符: %s", res.Body.String())
	}
	cd := list.Items[0]
	if cd.LunarLabel != "农历八月十五" || cd.NextDate == "" || cd.Days < 0 {
		t.Fatalf("农历换算不符: %+v", cd)
	}
	if res := memReq(r, token, "POST", base+"/countdowns", `{"name":"中秋","kind":"once","date":"2030-01-01"}`); res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "同名") {
		t.Fatalf("同名应 400: %d %s", res.Code, res.Body.String())
	}
	if res := memReq(r, token, "POST", base+"/countdowns", `{"name":"坏日期","date":"2030/01/01"}`); res.Code != http.StatusBadRequest {
		t.Fatalf("坏日期应 400: %s", res.Body.String())
	}
	// 一次性倒计时 3 天后 + 编辑改名
	in3 := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	res = memReq(r, token, "POST", base+"/countdowns", `{"name":"考试","kind":"once","date":"`+in3+`"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("新建应 200: %s", res.Body.String())
	}
	_ = json.Unmarshal(res.Body.Bytes(), &list)
	var examID int64
	for _, it := range list.Items {
		if it.Name == "考试" {
			examID = it.ID
			if it.Days != 3 {
				t.Fatalf("3 天后应 days=3，得 %d", it.Days)
			}
		}
	}
	if res := memReq(r, token, "PUT", base+"/countdowns/"+itoa(examID), `{"name":"期末考试","kind":"once","date":"`+in3+`"}`); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "期末考试") {
		t.Fatalf("编辑应 200 且改名: %d %s", res.Code, res.Body.String())
	}

	// 会员落日历
	expire := time.Now().AddDate(0, 0, 5).Format("2006-01-02")
	if res := memReq(r, token, "POST", base+"/memberships", `{"biz":"Netflix","name":"年费","expire_date":"`+expire+`"}`); res.Code != http.StatusOK {
		t.Fatalf("新建会员应 200: %s", res.Body.String())
	}
	from := time.Now().Format("2006-01-02")
	to := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	res = memReq(r, token, "GET", base+"/calendar?from="+from+"&to="+to, "")
	if res.Code != http.StatusOK {
		t.Fatalf("日历应 200: %s", res.Body.String())
	}
	var cal struct {
		Events []struct {
			Source string `json:"source"`
			Title  string `json:"title"`
			Date   string `json:"date"`
		} `json:"events"`
		Days []struct {
			Date       string `json:"date"`
			LunarMonth string `json:"lunar_month"`
			LunarDay   string `json:"lunar_day"`
		} `json:"days"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &cal); err != nil {
		t.Fatal(err)
	}
	if len(cal.Days) != 11 || cal.Days[0].Date != from || cal.Days[0].LunarMonth == "" || cal.Days[0].LunarDay == "" {
		t.Fatalf("日标记不符: %d %+v", len(cal.Days), cal.Days[:1])
	}
	gotCD, gotMem := false, false
	for _, e := range cal.Events {
		if e.Source == "countdown" && e.Title == "期末考试" && e.Date == in3 {
			gotCD = true
		}
		if e.Source == "membership" && e.Title == "Netflix" && e.Date == expire {
			gotMem = true
		}
	}
	if !gotCD || !gotMem {
		t.Fatalf("日历缺倒数日/会员事件: %+v", cal.Events)
	}
	if res := memReq(r, token, "GET", base+"/calendar?from=x&to=y", ""); res.Code != http.StatusBadRequest {
		t.Fatalf("坏日期应 400: %d", res.Code)
	}

	// 助理设置
	res = memReq(r, token, "PUT", base+"/assistant/settings", `{"brief_on":false,"brief_time":"7:45","warn_on":true,"umbrella_on":true,"umbrella_time":"21:00","umbrella_threshold":60,"air_on":true,"air_threshold":200}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"brief_time":"07:45"`) {
		t.Fatalf("设置回写不符: %d %s", res.Code, res.Body.String())
	}
	if res := memReq(r, token, "PUT", base+"/assistant/settings", `{"brief_time":"25:00","umbrella_time":"20:00","umbrella_threshold":50,"air_threshold":150}`); res.Code != http.StatusBadRequest {
		t.Fatalf("非法时间应 400: %d", res.Code)
	}
	if res := memReq(r, token, "GET", base+"/assistant/brief-preview", ""); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "每日早报") {
		t.Fatalf("早报预览不符: %d %s", res.Code, res.Body.String())
	}
	// 三渠道助理开关走 /settings
	if res := memReq(r, token, "PUT", base+"/settings", `{"assist_wx_on":false}`); res.Code != http.StatusOK {
		t.Fatalf("保存助理渠道开关应 200: %s", res.Body.String())
	}
	if res := memReq(r, token, "GET", base+"/settings", ""); !strings.Contains(res.Body.String(), `"assist_wx_on":false`) || !strings.Contains(res.Body.String(), `"assist_wh_on":true`) {
		t.Fatalf("设置视图缺助理开关: %s", res.Body.String())
	}

	// ICS 导出：无令牌 404 → feed-info 生成 → 200 text/calendar 含会员事件 → 重置后旧令牌 404
	if w := memReq(r, "", "GET", base+"/calendar/feed.ics?token=abc", ""); w.Code != http.StatusNotFound {
		t.Fatalf("无令牌应 404，得 %d", w.Code)
	}
	res = memReq(r, token, "GET", base+"/calendar/feed-info", "")
	var info struct {
		Token string `json:"token"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &info); err != nil || len(info.Token) != 64 {
		t.Fatalf("feed-info 不符: %s", res.Body.String())
	}
	w := memReq(r, "", "GET", info.Path+"?token="+info.Token, "")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/calendar") {
		t.Fatalf("订阅应 200 text/calendar: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	for _, want := range []string{"BEGIN:VCALENDAR", "SUMMARY:Netflix 会员到期", "SUMMARY:期末考试", "SUMMARY:中秋", "END:VCALENDAR"} {
		if !strings.Contains(body, want) {
			t.Fatalf("ICS 缺 %q:\n%s", want, body)
		}
	}
	if w := memReq(r, "", "GET", info.Path+"?token="+strings.Repeat("0", 64), ""); w.Code != http.StatusNotFound {
		t.Fatalf("错令牌应 404，得 %d", w.Code)
	}
	res = memReq(r, token, "POST", base+"/calendar/feed-token", "")
	var rotated struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &rotated)
	if rotated.Token == "" || rotated.Token == info.Token {
		t.Fatalf("重置应换新令牌: %s", res.Body.String())
	}
	if w := memReq(r, "", "GET", info.Path+"?token="+info.Token, ""); w.Code != http.StatusNotFound {
		t.Fatalf("旧令牌应失效 404，得 %d", w.Code)
	}
	// 来源开关
	if res := memReq(r, token, "PUT", base+"/calendar/feed-sources", `{"reminders":true,"memberships":false,"countdowns":true,"tv":false,"certs":true}`); res.Code != http.StatusOK {
		t.Fatalf("来源开关应 200: %s", res.Body.String())
	}
	w = memReq(r, "", "GET", info.Path+"?token="+rotated.Token, "")
	if strings.Contains(w.Body.String(), "Netflix 会员到期") {
		t.Fatal("关闭会员来源后不应再导出会员事件")
	}

	// 订阅：坏地址 400；空清单 200
	if res := memReq(r, token, "POST", base+"/calendar/subs", `{"name":"x","url":"ftp://a/b.ics"}`); res.Code != http.StatusBadRequest {
		t.Fatalf("非 http(s) 应 400: %d %s", res.Code, res.Body.String())
	}
	if res := memReq(r, token, "GET", base+"/calendar/subs", ""); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"items":[]`) {
		t.Fatalf("空订阅清单应 200 []: %d %s", res.Code, res.Body.String())
	}

	// 删除倒数日
	if res := memReq(r, token, "DELETE", base+"/countdowns/"+itoa(examID), ""); res.Code != http.StatusOK {
		t.Fatalf("删除应 200: %s", res.Body.String())
	}
	res = memReq(r, token, "GET", base+"/countdowns", "")
	if strings.Contains(res.Body.String(), "期末考试") {
		t.Fatal("删除后不应再出现")
	}
}
