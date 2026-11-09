package collect

import (
	"strings"
	"testing"
)

func TestParseCPUStat(t *testing.T) {
	fixture := strings.NewReader(
		"cpu  100 0 50 800 20 0 10 0 0 0\n" +
			"cpu0 50 0 25 400 10 0 5 0 0 0\n")
	times, err := parseCPUStat(fixture)
	if err != nil {
		t.Fatal(err)
	}
	// busy = 100+0+50+0+10+0 = 160, idle+iowait = 800+20 = 820
	if times.busy != 160 || times.total != 980 {
		t.Fatalf("busy=%d total=%d, 期望 160/980", times.busy, times.total)
	}
}

func TestCPUPercent(t *testing.T) {
	cases := []struct {
		prev, cur cpuTimes
		want      float64
	}{
		{cpuTimes{busy: 100, total: 200}, cpuTimes{busy: 150, total: 250}, 100}, // 差值全忙
		{cpuTimes{busy: 100, total: 200}, cpuTimes{busy: 110, total: 300}, 10},  // 10/100
		{cpuTimes{busy: 100, total: 200}, cpuTimes{busy: 100, total: 200}, 0},   // 无变化
	}
	for _, c := range cases {
		if got := cpuPercent(c.prev, c.cur); got != c.want {
			t.Errorf("cpuPercent(%v, %v) = %v, 期望 %v", c.prev, c.cur, got, c.want)
		}
	}
}

func TestParseCPUStatMissing(t *testing.T) {
	if _, err := parseCPUStat(strings.NewReader("cpu0 1 1 1 1 1 1 1 1\n")); err == nil {
		t.Fatal("缺少汇总行应报错")
	}
}
