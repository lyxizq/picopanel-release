// Package collect 实现 agent 侧的指标采集。
//
// 所有采集器由主循环在单个 goroutine 中顺序调用（有状态的差值采集器
// 因此无需加锁）；解析函数统一接收 io.Reader（生产传 *os.File，
// 测试传 fixture 字符串），便于单元测试。
package collect

import (
	"os"

	"lz-monitor/internal/proto"
)

// Collector 采集一项指标并写入 Report。
// 有状态的采集器（CPU/网络差值）自行保存上一次原始值。
type Collector interface {
	Name() string
	Collect(r *proto.Report) error
}

// open 打开 /proc 下的文件，统一错误信息。
func open(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return f, nil
}
