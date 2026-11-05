package proto

import "testing"

func TestParseHexSockAddr(t *testing.T) {
	// IPv4：0100007F 小端 = 127.0.0.1，端口 1F90 = 8080
	addr, port, ok := ParseHexSockAddr("0100007F:1F90", false)
	if !ok || addr != "127.0.0.1" || port != 8080 {
		t.Fatalf("v4 = %q %d %v", addr, port, ok)
	}
	// IPv4 通配
	addr, port, ok = ParseHexSockAddr("00000000:0016", false)
	if !ok || addr != "0.0.0.0" || port != 22 {
		t.Fatalf("v4 通配 = %q %d %v", addr, port, ok)
	}
	// IPv6：32 组十六进制小端，全零 = ::
	addr, port, ok = ParseHexSockAddr("00000000000000000000000000000000:0016", true)
	if !ok || addr != "::" || port != 22 {
		t.Fatalf("v6 通配 = %q %d %v", addr, port, ok)
	}
	// IPv6 环回（最后一组 01000000 小端 = ::1）
	addr, _, ok = ParseHexSockAddr("00000000000000000000000001000000:0035", true)
	if !ok || addr != "::1" {
		t.Fatalf("v6 环回 = %q %v", addr, ok)
	}
	// 非法输入
	if _, _, ok := ParseHexSockAddr("ZZZZZZZZ:0016", false); ok {
		t.Fatal("非法 v4 应失败")
	}
	if _, _, ok := ParseHexSockAddr("0100007F:0000", false); ok {
		t.Fatal("端口 0 应失败")
	}
}

func TestBuildListenList(t *testing.T) {
	entries := []ListenEntry{
		{Proto: "tcp", Addr: "127.0.0.1", Port: 8080}, // 仅环回 → 整个端口丢弃
		{Proto: "tcp", Addr: "::1", Port: 8080},       // 仅环回 → 丢弃
		{Proto: "tcp", Addr: "0.0.0.0", Port: 22},
		{Proto: "tcp", Addr: "::", Port: 22},         // 双栈合并
		{Proto: "tcp", Addr: "0.0.0.0", Port: 22},    // 地址去重
		{Proto: "tcp", Addr: "127.0.0.1", Port: 443}, // 环回 + 通配并存 → 只留通配
		{Proto: "tcp", Addr: "0.0.0.0", Port: 443},
		{Proto: "udp", Addr: "127.0.0.2", Port: 53}, // 仅环回（127.0.0.0/8）→ 丢弃
		{Proto: "", Addr: "0.0.0.0", Port: 9999},    // 非法 proto 丢弃
		{Proto: "tcp", Addr: "0.0.0.0", Port: 0},    // 端口 0 丢弃
	}
	list := BuildListenList(entries, 256)
	if len(list) != 2 {
		t.Fatalf("条目数 = %d, 期望 2: %+v", len(list), list)
	}
	// tcp 在前按端口升序：22, 443（8080/53 仅环回已被过滤）
	want := []uint16{22, 443}
	for i, lp := range list {
		if lp.Port != want[i] {
			t.Fatalf("第 %d 个端口 = %d, 期望 %d", i, lp.Port, want[i])
		}
	}
	if len(list[0].Addrs) != 2 {
		t.Fatalf("tcp/22 地址应合并为 2 个: %+v", list[0].Addrs)
	}
	if len(list[1].Addrs) != 1 || list[1].Addrs[0] != "0.0.0.0" {
		t.Fatalf("tcp/443 应只留通配地址: %+v", list[1].Addrs)
	}
	// 截断
	if got := len(BuildListenList(entries, 1)); got != 1 {
		t.Fatalf("max=1 截断后 = %d", got)
	}
}
