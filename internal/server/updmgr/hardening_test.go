package updmgr

import (
	"testing"
	"time"
)

// push 待领指令 24h 过期：目标机器升级一直失败时不再无限重拉分发束。
func TestPushInstructionExpires(t *testing.T) {
	m := New(nil, Config{Version: "v1.0.1"})
	m.mu.Lock()
	m.pending[7] = pushPending{version: "v1.0.2", at: time.Now().Add(-25 * time.Hour)}
	m.pending[8] = pushPending{version: "v1.0.2", at: time.Now()}
	m.mu.Unlock()
	if m.PushInstruction(7) != nil {
		t.Error("过期指令应返回 nil 不下发")
	}
	if m.PushInstruction(8) == nil {
		t.Error("未过期指令应正常下发")
	}
	m.mu.Lock()
	_, ok := m.pending[7]
	m.mu.Unlock()
	if ok {
		t.Error("过期条目应被懒清理")
	}
}
