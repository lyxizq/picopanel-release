package proto

import "testing"

func TestAdjustContainerMem(t *testing.T) {
	const GB = 1024 * 1024 * 1024
	total, used := uint64(8*GB), uint64(2*GB)

	// 自身与 PID 1 不同 cgroup（systemd 服务单元）→ 不修正
	if tt, uu := AdjustContainerMem(total, used,
		"0::/system.slice/lz-agent.service\n", "0::/init.scope\n",
		"1073741824\n", "536870912\n", "", ""); tt != total || uu != used {
		t.Fatalf("非容器根不应修正: %d/%d", tt, uu)
	}

	// cgroup v2 限额生效：以限额为总量、cgroup 用量为已用
	if tt, uu := AdjustContainerMem(total, used,
		"0::/\n", "0::/\n",
		"1073741824\n", "536870912\n", "", ""); tt != 1*GB || uu != 512*1024*1024 {
		t.Fatalf("v2 限额未生效: %d/%d", tt, uu)
	}

	// v2 "max"（无限制）+ v1 巨值 → 不修正
	if tt, uu := AdjustContainerMem(total, used,
		"0::/\n", "0::/\n",
		"max\n", "536870912\n", "9223372036854771712\n", "1024\n"); tt != total || uu != used {
		t.Fatalf("无限制不应修正: %d/%d", tt, uu)
	}

	// cgroup v1 回退：用量超限时夹紧到限额
	if tt, uu := AdjustContainerMem(total, used,
		"11:memory:/lxc/web\n0::/\n", "11:memory:/lxc/web\n0::/\n",
		"", "", "536870912\n", "600000000\n"); tt != 512*1024*1024 || uu != 512*1024*1024 {
		t.Fatalf("v1 限额未生效: %d/%d", tt, uu)
	}

	// 限额不小于 MemTotal（裸机 root cgroup）→ 不修正；空 cgroup 路径同理
	if tt, uu := AdjustContainerMem(total, used, "", "0::/\n", "1073741824\n", "1\n", "", ""); tt != total || uu != used {
		t.Fatalf("空 selfCG 不应修正: %d/%d", tt, uu)
	}
}
