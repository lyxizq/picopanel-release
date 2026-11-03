package certcheck

import (
	"path/filepath"
	"testing"

	"lz-monitor/internal/server/store"
)

// TestCheckOneDialFailure 拨号失败路径：127.0.0.1:1 连接拒绝保证测试不碰外网。
// 失败回写 last_error 并清空结果字段（下次检查前不再当有可信到期时间）。
func TestCheckOneDialFailure(t *testing.T) {
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "certcheck.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.CreateCert("127.0.0.1", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	// 上一轮成功检查的结果先落库
	if err := s.SaveCertCheck(id, store.CertResult{
		NotBefore: 1700000000, NotAfter: 1800000000,
		Issuer: "R11", IssuedTo: "127.0.0.1", Subject: "127.0.0.1",
	}, ""); err != nil {
		t.Fatal(err)
	}

	certs, err := s.ListCerts()
	if err != nil || len(certs) != 1 {
		t.Fatalf("查询清单: %v %d", err, len(certs))
	}
	CheckOne(s, &certs[0])

	c, err := s.GetCert(id)
	if err != nil {
		t.Fatal(err)
	}
	if c.LastError == "" {
		t.Fatal("拨号失败应回写 last_error")
	}
	if c.NotAfter != 0 || c.Issuer != "" {
		t.Fatalf("失败应清空结果字段（不再可信），得到 not_after=%d issuer=%q", c.NotAfter, c.Issuer)
	}
	if c.LastCheck == 0 {
		t.Fatal("失败也应刷新 last_check")
	}
}
