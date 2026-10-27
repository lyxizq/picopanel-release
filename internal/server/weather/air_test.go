package weather

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 包级 init：把空气质量兜底端点指向本地桩，让全包既有测试（Get 三路径都会走
// fillAir）不再真打 Open-Meteo（否则沙箱里撞 10s 超时拖慢整包）。
func init() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"current":{"pm2_5":80,"pm10":120}}`))
	}))
	airQualityURL = srv.URL + "/?lat=%.4f&lon=%.4f"
}

// TestAirFromPM 国标 IAQI 分段线性换算：PM2.5 80 → 107（轻度污染，首要 PM2.5）；
// PM10 120 → 85；取大者；空气优时首要污染物置空；两者皆缺返回 nil。
func TestAirFromPM(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	a := airFromPM(f(80), f(120))
	if a == nil || a.Aqi != 107 || a.Category != "轻度污染" || a.Primary != "PM2.5" || !a.Approx {
		t.Fatalf("PM2.5=80/PM10=120 换算不符: %+v", a)
	}
	if a := airFromPM(f(10), nil); a == nil || a.Aqi != 15 || a.Category != "优" || a.Primary != "" {
		t.Fatalf("PM2.5=10 应为 AQI 15 优且无首要污染物: %+v", a)
	}
	if a := airFromPM(nil, f(700)); a == nil || a.Aqi != 500 || a.Category != "严重污染" {
		t.Fatalf("超高浓度应封顶 500: %+v", a)
	}
	if airFromPM(nil, nil) != nil {
		t.Fatal("两者皆缺应为 nil")
	}
	for aqi, want := range map[int]string{50: "优", 100: "良", 150: "轻度污染", 200: "中度污染", 300: "重度污染", 301: "严重污染"} {
		if got := AqiCategory(aqi); got != want {
			t.Fatalf("AQI %d 等级 %q，期望 %q", aqi, got, want)
		}
	}
}

// TestFillAirFallback 服务商无 Air 时经兜底补齐（走包级桩），已有 Air 不覆盖。
func TestFillAirFallback(t *testing.T) {
	r := &Resp{Lat: 39.9, Lon: 116.4}
	fillAir(r)
	if r.Air == nil || r.Air.Aqi != 107 || !r.Air.Approx {
		t.Fatalf("兜底未补齐: %+v", r.Air)
	}
	keep := &Resp{Lat: 1, Lon: 1, Air: &Air{Aqi: 42, Category: "优"}}
	fillAir(keep)
	if keep.Air.Aqi != 42 || keep.Air.Approx {
		t.Fatalf("服务商自带 Air 不应被覆盖: %+v", keep.Air)
	}
}
