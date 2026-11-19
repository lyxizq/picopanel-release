package assistant

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sample2027 = `{"$schema":"x","year":2027,"days":[
 {"name":"元旦","date":"2027-01-03","isOffDay":true},
 {"name":"元旦","date":"2027-01-01","isOffDay":true},
 {"name":"元旦","date":"2027-01-02","isOffDay":true},
 {"name":"春节","date":"2027-02-05","isOffDay":true},
 {"name":"春节","date":"2027-02-06","isOffDay":true},
 {"name":"春节调休上班","date":"2027-01-31","isOffDay":false},
 {"name":"坏日期","date":"2027-13-40","isOffDay":true}
]}`

// 测试后把 2027 从合并视图撤掉，免得污染 TestHolidays 的「2027 未内置」断言。
func dropYear(t *testing.T, y int) {
	t.Cleanup(func() {
		holMu.Lock()
		delete(holYears, y)
		rebuildHolidayView()
		holMu.Unlock()
	})
}

func TestParseHolidayCN(t *testing.T) {
	days, err := parseHolidayCN([]byte(sample2027), 2027)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 6 || days[0].Date != "2027-01-01" || days[5].Date != "2027-02-06" {
		t.Fatalf("应 6 条按日期升序（坏日期剔除）: %+v", days)
	}
	if _, err := parseHolidayCN([]byte(sample2027), 2026); err == nil || !strings.Contains(err.Error(), "年份不符") {
		t.Fatalf("年份不符应报错: %v", err)
	}
	if _, err := parseHolidayCN([]byte(`{"year":2027,"days":[]}`), 2027); err == nil {
		t.Fatal("空清单应报错")
	}
	if _, err := parseHolidayCN([]byte(`not json`), 2027); err == nil {
		t.Fatal("非 JSON 应报错")
	}
}

func TestRemoteYearDataGrouping(t *testing.T) {
	days, _ := parseHolidayCN([]byte(sample2027), 2027)
	d := remoteYearData(days, 123)
	if d.source != "remote" || d.fetchedAt != 123 {
		t.Fatalf("来源/时间不符: %+v", d)
	}
	if len(d.periods) != 2 || d.periods[0].name != "元旦" || len(d.periods[0].days) != 3 || len(d.periods[1].days) != 2 {
		t.Fatalf("连续放假日应并成两段（元旦 3 天 / 春节 2 天）: %+v", d.periods)
	}
	if m := d.marks["2027-01-02"]; !m.Off || m.Index != 2 || m.Total != 3 || m.Name != "元旦" {
		t.Fatalf("1/2 应为元旦第 2/3 天: %+v", m)
	}
	if m := d.marks["2027-01-31"]; m.Off || m.Name != "春节调休上班" {
		t.Fatalf("1/31 应为调休上班: %+v", m)
	}
}

func TestFetchHolidayCNFallbackAndSync(t *testing.T) {
	calls := map[string]int{}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls["bad"]++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls["good"]++
		if strings.Contains(r.URL.Path, "2027") {
			_, _ = w.Write([]byte(sample2027))
			return
		}
		w.WriteHeader(http.StatusNotFound) // 2026 远程无数据 → 内置保留
	}))
	defer good.Close()
	old := holidayCNURLs
	holidayCNURLs = []string{bad.URL + "/%d.json", good.URL + "/%d.json"}
	t.Cleanup(func() { holidayCNURLs = old })
	dropYear(t, 2027)

	// 首源 500 → 落到第二源
	days, err := fetchHolidayCN(2027)
	if err != nil || len(days) != 6 {
		t.Fatalf("回退取数失败: %v %d", err, len(days))
	}
	if calls["bad"] != 1 || calls["good"] != 1 {
		t.Fatalf("调用次序不符: %v", calls)
	}

	st := newStore(t)
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.Local)
	if err := SyncHolidayCN(st, now); err != nil {
		t.Fatalf("有年份成功应返回 nil: %v", err)
	}
	status := GetHolidayStatus(st)
	if status.SyncAt != now.Unix() || status.Error != "" {
		t.Fatalf("同步状态不符: %+v", status)
	}
	if !HolidayKnownYear(2027) {
		t.Fatal("2027 应已入合并视图")
	}
	if m, ok := HolidayOf("2027-01-01"); !ok || !m.Off || m.Total != 3 {
		t.Fatalf("2027-01-01 应为元旦第 1/3 天: %+v %v", m, ok)
	}
	// 2026 内置未被 404 覆盖
	if m, ok := HolidayOf("2026-10-01"); !ok || m.Name != "国庆节" {
		t.Fatalf("2026 内置应保留: %+v %v", m, ok)
	}
	// 早报假期行跨年：2026-12-31 → 明天起元旦放假 3 天
	if got := HolidayLine(time.Date(2026, 12, 31, 9, 0, 0, 0, sh)); got != "明天起元旦放假 3 天" {
		t.Fatalf("跨年假期行不符: %q", got)
	}
	// 弹层快照含来源与条目
	var found bool
	for _, y := range status.Years {
		if y.Year == 2027 {
			found = y.Source == "remote" && y.FetchedAt == now.Unix() && len(y.Days) == 6
		}
		if y.Year == 2026 && (y.Source != "builtin" || len(y.Days) == 0) {
			t.Fatalf("2026 应为内置且带清单: %+v", y.Source)
		}
	}
	if !found {
		t.Fatalf("快照缺 2027 远程年份: %+v", status.Years)
	}

	// 缓存回装：清空视图后 LoadHolidayCache 从 settings 恢复 2027
	holMu.Lock()
	delete(holYears, 2027)
	rebuildHolidayView()
	holMu.Unlock()
	LoadHolidayCache(st, now)
	if !HolidayKnownYear(2027) {
		t.Fatal("缓存回装失败")
	}

	// 全部失败：只记错误，已有数据保留
	holidayCNURLs = []string{bad.URL + "/%d.json"}
	if err := SyncHolidayCN(st, now.Add(time.Hour)); err == nil {
		t.Fatal("全部失败应返回错误")
	}
	status = GetHolidayStatus(st)
	if status.Error == "" || status.SyncAt != now.Unix() || !HolidayKnownYear(2027) {
		t.Fatalf("失败后应记错误且保留旧数据/旧同步时刻: %+v", status)
	}
}
