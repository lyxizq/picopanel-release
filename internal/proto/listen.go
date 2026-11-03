package proto

import (
	"net"
	"sort"
	"strconv"
	"strings"
)

// ListenEntry 是一条监听socket 的原始采集条目（agent 读 /proc/net/tcp* 与
// ssh 模式解析远端 awk 输出都先得到这个，再经 BuildListenList 汇总）。
type ListenEntry struct {
	Proto string // "tcp" / "udp"
	Addr  string // 可读 IP（0.0.0.0 / 127.0.0.1 / :: …）
	Port  uint16
}

// ParseHexSockAddr 解析 /proc/net/{tcp,tcp6,udp,udp6} 的地址列（十六进制
// "IP:PORT"，如 IPv4 "0100007F:1F90"、IPv6 32 位十六进制组），内核按
// 小端序存放，这里转回可读 IP 与端口。
func ParseHexSockAddr(s string, v6 bool) (addr string, port uint16, ok bool) {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return "", 0, false
	}
	p, err := strconv.ParseUint(s[i+1:], 16, 32)
	if err != nil || p == 0 || p > 65535 {
		return "", 0, false
	}
	port = uint16(p)
	h := s[:i]
	if v6 {
		if len(h) != 32 {
			return "", 0, false
		}
		b := make([]byte, 16)
		for g := 0; g < 4; g++ {
			v, err := strconv.ParseUint(h[g*8:(g+1)*8], 16, 64)
			if err != nil {
				return "", 0, false
			}
			// 每个 32 位组内部小端
			b[g*4], b[g*4+1], b[g*4+2], b[g*4+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
		}
		return net.IP(b).String(), port, true
	}
	if len(h) != 8 {
		return "", 0, false
	}
	v, err := strconv.ParseUint(h, 16, 64)
	if err != nil {
		return "", 0, false
	}
	return net.IP{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}.String(), port, true
}

// BuildListenList 把原始条目汇总为上报用的监听列表：同 proto+port 的地址
// 去重合并，tcp 在前按端口排序，超过 max 条截断（防异常机器刷爆存储）。
// 环回地址（127.0.0.0/8 与 ::1）仅本机可访问，不纳入列表；端口同时在
// 非环回地址监听时仍保留（仅去掉环回地址条目）。
func BuildListenList(entries []ListenEntry, max int) []ListenPort {
	type key struct {
		proto string
		port  uint16
	}
	order := make([]key, 0, len(entries))
	m := map[key]*ListenPort{}
	for _, e := range entries {
		if e.Port == 0 || (e.Proto != "tcp" && e.Proto != "udp") {
			continue
		}
		if isLoopbackAddr(e.Addr) {
			continue
		}
		k := key{e.Proto, e.Port}
		lp, seen := m[k]
		if !seen {
			lp = &ListenPort{Proto: e.Proto, Port: e.Port}
			m[k] = lp
			order = append(order, k)
		}
		dup := false
		for _, a := range lp.Addrs {
			if a == e.Addr {
				dup = true
				break
			}
		}
		if !dup {
			lp.Addrs = append(lp.Addrs, e.Addr)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].proto != order[j].proto {
			return order[i].proto < order[j].proto // tcp < udp
		}
		return order[i].port < order[j].port
	})
	n := len(order)
	if max > 0 && n > max {
		n = max
	}
	out := make([]ListenPort, 0, n)
	for _, k := range order[:n] {
		out = append(out, *m[k])
	}
	return out
}

// isLoopbackAddr 判断监听地址是否环回（IPv4 127.0.0.0/8 或 IPv6 ::1）。
func isLoopbackAddr(addr string) bool {
	return strings.HasPrefix(addr, "127.") || addr == "::1"
}
