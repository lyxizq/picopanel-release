package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"lz-monitor/internal/proto"
)

func openBatchStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// trafficBooked 查当日账本指定网口已入账字节数（无行=0）。
func trafficBooked(t *testing.T, s *Store, serverID int64, iface string) (int64, int64) {
	t.Helper()
	var rx, tx sql.NullInt64
	err := s.db.QueryRow(`SELECT rx_bytes, tx_bytes FROM net_traffic_days
		WHERE server_id = ? AND day = ? AND iface = ?`,
		serverID, time.Now().Format("2006-01-02"), iface).Scan(&rx, &tx)
	if err == sql.ErrNoRows {
		return 0, 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return rx.Int64, tx.Int64
}

// batchSamples 造一批 3 份样本：agent TS 从 baseTS 起等距 10s 递增，计数器从
// counterBase 起单调递增（真实 agent 跨批计数器只增不减）。
func batchSamples(baseTS, counterBase int64) []proto.Report {
	mk := func(i int64) proto.Report {
		return proto.Report{
			TS:  baseTS + i*10,
			CPU: float64(i),
			Nets: []proto.NetInfo{{
				Iface: "eth0", RxBytes: uint64(counterBase + i*100), TxBytes: uint64(counterBase/2 + i*50),
			}},
		}
	}
	return []proto.Report{mk(0), mk(1), mk(2)}
}

// TestIngestBatchReplayIdempotent 重放幂等：同批样本二次入库（响应丢失后
// agent 重发），水位以内的旧样本跳过——不产生重复指标行，更不允许流量差分
// 链把重放样本的计数器回退误当「网卡归零」全额入账。
func TestIngestBatchReplayIdempotent(t *testing.T) {
	s := openBatchStore(t)
	row, err := s.CreateServer("push-1", "", "tok", 0)
	if err != nil {
		t.Fatal(err)
	}
	samples := batchSamples(time.Now().Unix(), 1000)

	if n, err := s.IngestBatch(row.ID, time.Now().Unix(), samples, 10); err != nil || n != 3 {
		t.Fatalf("首批应入库 3 份: n=%d err=%v", n, err)
	}
	rx1, tx1 := trafficBooked(t, s, row.ID, "eth0")
	if rx1 != 200 || tx1 != 100 { // (s2-s1)+(s3-s2) = 100+100 rx / 50+50 tx
		t.Fatalf("首批入账应恰为两段差值: rx=%d tx=%d", rx1, tx1)
	}

	// 同批重放：t1/t2 低于水位跳过；t3 与水位相等按新样本重插（流量差值 0）
	if n, err := s.IngestBatch(row.ID, time.Now().Unix(), samples, 10); err != nil || n != 1 {
		t.Fatalf("重放只应重插等水位末位 1 份: n=%d err=%v", n, err)
	}
	rx2, tx2 := trafficBooked(t, s, row.ID, "eth0")
	if rx2 != rx1 || tx2 != tx1 {
		t.Fatalf("重放不得改动流量账本: %d/%d → %d/%d", rx1, tx1, rx2, tx2)
	}
}

// TestIngestBatchSupersetRetry 失败留存后的超集重试：响应丢失时服务端已入库
// t1..t3，agent 留存续攒后重发 t1..t6——t1/t2 水位内跳过，仅新增 t4..t6，
// 流量差分链从 t3 基线继续正确入账（绝无回退全额误入账）。
func TestIngestBatchSupersetRetry(t *testing.T) {
	s := openBatchStore(t)
	row, err := s.CreateServer("push-2", "", "tok", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Unix()
	first := batchSamples(base, 1000)
	if _, err := s.IngestBatch(row.ID, time.Now().Unix(), first, 10); err != nil {
		t.Fatal(err)
	}
	rx1, _ := trafficBooked(t, s, row.ID, "eth0")

	// 超集 = t1..t3 留存 + t4..t6 新采（计数器继续递增）
	superset := append(append([]proto.Report{}, first...), batchSamples(base+30, 1300)...)
	if n, err := s.IngestBatch(row.ID, time.Now().Unix(), superset, 10); err != nil || n != 4 {
		t.Fatalf("超集重试应只入库 t3(等值重插)+t4..t6 共 4 份: n=%d err=%v", n, err)
	}
	rx2, _ := trafficBooked(t, s, row.ID, "eth0")
	if rx2-rx1 != 300 { // t3 差值 0 + t4/t5/t6 各 100
		t.Fatalf("超集重试流量入账错误: %d → %d（增量应 300）", rx1, rx2)
	}
}

// TestIngestBatchStaleClockNotSwallowed 窗口外的陈旧 agent TS 不被水位吞掉
//（agent 时钟跳变保护：宁可重复不可丢数据）。
func TestIngestBatchStaleClockNotSwallowed(t *testing.T) {
	s := openBatchStore(t)
	row, err := s.CreateServer("push-3", "", "tok", 0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if _, err := s.IngestBatch(row.ID, now, batchSamples(now, 1000), 10); err != nil {
		t.Fatal(err)
	}
	// 时钟回拨 1 小时的样本：与水位差远超 600s 窗口，按新样本入库
	stale := batchSamples(now-3600, 2000)
	if n, err := s.IngestBatch(row.ID, now, stale, 10); err != nil || n != 3 {
		t.Fatalf("窗口外陈旧样本应照常入库 3 份: n=%d err=%v", n, err)
	}
}
