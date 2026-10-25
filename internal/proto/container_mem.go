package proto

import (
	"strconv"
	"strings"
)

// AdjustContainerMem 修正容器环境下的内存口径。
//
// 部分虚拟化环境（未挂 lxcfs 的 LXC、部分 OpenVZ）里 /proc/meminfo 反映的是宿主机
// 内存而非本容器，直接按 /proc/meminfo 计算会严重失真。若自身与 PID 1 位于
// 同一 cgroup（容器里成立；裸机 root cgroup 无限额、systemd 服务单元与 init 不同，
// 均自动跳过）且 cgroup 内存限额小于 MemTotal，则改用 cgroup 的限额与用量。
//
// 参数均为文件内容文本（agent 本地读取 / SSH 模式远端 cat 回来），读取失败传空串；
// 返回修正后的 (total, used)，不满足修正条件时原样返回。
func AdjustContainerMem(total, used uint64, selfCG, initCG, v2Max, v2Cur, v1Limit, v1Usage string) (uint64, uint64) {
	self := strings.TrimSpace(selfCG)
	if total == 0 || self == "" || self != strings.TrimSpace(initCG) {
		return total, used
	}
	limit, cur := uint64(0), uint64(0)
	// cgroup v2：memory.max；"max"（无限制）非数字，解析失败自动跳过
	if m, err := strconv.ParseUint(strings.TrimSpace(v2Max), 10, 64); err == nil && m > 0 && m < total {
		limit = m
		cur, _ = strconv.ParseUint(strings.TrimSpace(v2Cur), 10, 64)
	} else if l, err := strconv.ParseUint(strings.TrimSpace(v1Limit), 10, 64); err == nil && l > 0 && l < total {
		// cgroup v1：无限制时为一个接近 2^63 的巨值，不会小于 MemTotal
		limit = l
		cur, _ = strconv.ParseUint(strings.TrimSpace(v1Usage), 10, 64)
	}
	if limit == 0 {
		return total, used
	}
	if cur > limit {
		cur = limit
	}
	return limit, cur
}
