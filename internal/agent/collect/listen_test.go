package collect

import (
	"strings"
	"testing"

	"lz-monitor/internal/proto"
)

func TestParseListenRowsTCP(t *testing.T) {
	fixture := strings.NewReader(
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
			"   0: 0100007F:0035 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0\n" +
			"   1: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12346 1 0000000000000000 100 0 0 10 0\n" +
			"   2: 0B00000A:8AE3 0C00000A:01BB 01 00000000:00000000 02:00000000 00000000     0        0 23456 1 0000000000000000 20 4 30 10 -1\n")
	entries := parseListenRows(fixture, "tcp", false, true)
	if len(entries) != 2 { // ESTABLISHED 行不计
		t.Fatalf("LISTEN 条目 = %d, 期望 2: %+v", len(entries), entries)
	}
	if entries[0].Addr != "127.0.0.1" || entries[0].Port != 53 {
		t.Fatalf("第一条 = %+v", entries[0])
	}
	if entries[1].Addr != "0.0.0.0" || entries[1].Port != 22 {
		t.Fatalf("第二条 = %+v", entries[1])
	}
}

func TestParseListenRowsUDP(t *testing.T) {
	fixture := strings.NewReader(
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
			"   0: 0100007F:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0\n" +
			"   1: 0B00000A:C350 0C00000A:0035 01 00000000:00000000 00:00000000 00000000     0        0 23456 1 0000000000000000 20 4 30 10 -1\n")
	entries := parseListenRows(fixture, "udp", false, false)
	if len(entries) != 1 { // 已连接的 UDP（remote 非零）不计
		t.Fatalf("UDP 绑定条目 = %d, 期望 1: %+v", len(entries), entries)
	}
	if entries[0].Proto != "udp" || entries[0].Port != 53 {
		t.Fatalf("条目 = %+v", entries[0])
	}
}

func TestListenCollectorBuild(t *testing.T) {
	// Collect 在 macOS 测试机上 /proc 不存在，验证其仍产出合法空列表（不报错）
	c := NewListenCollector()
	rep := &proto.Report{}
	if err := c.Collect(rep); err != nil {
		t.Fatal(err)
	}
	if rep.Listens == nil {
		t.Fatal("Listens 不应为 nil（nil 表示旧版 agent，语义不同）")
	}
}
