package collect

import (
	"strings"
	"testing"
)

func TestParseLoadAvg(t *testing.T) {
	l1, l5, l15, err := parseLoadAvg(strings.NewReader("0.52 0.58 0.59 1/467 12345\n"))
	if err != nil {
		t.Fatal(err)
	}
	if l1 != 0.52 || l5 != 0.58 || l15 != 0.59 {
		t.Fatalf("load = %v/%v/%v", l1, l5, l15)
	}
}
