package collect

import (
	"strings"
	"testing"
)

func TestCountEstablished(t *testing.T) {
	fixture := strings.NewReader(
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
			"   0: 0100007F:0035 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0\n" +
			"   1: 0B00000A:8AE3 0C00000A:01BB 01 00000000:00000000 02:00000000 00000000     0        0 23456 1 0000000000000000 20 4 30 10 -1\n" +
			"   2: 0B00000A:8AE4 0C00000A:01BB 01 00000000:00000000 02:00000000 00000000     0        0 23457 1 0000000000000000 20 4 30 10 -1\n")
	if n := countEstablished(fixture); n != 2 {
		t.Fatalf("ESTABLISHED = %d, 期望 2", n)
	}
}

func TestCountEstablishedEmpty(t *testing.T) {
	if n := countEstablished(strings.NewReader("sl local rem st\n")); n != 0 {
		t.Fatalf("空表 = %d", n)
	}
}
