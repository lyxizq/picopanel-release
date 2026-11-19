package dbtool

import (
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

func hexEncode(b []byte) string { return strings.ToUpper(hex.EncodeToString(b)) }

// isPrintableUTF8 是否可直接当文本展示（合法 UTF-8 且无控制字符）。
func isPrintableUTF8(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

// idword 标识符合法性预检：拒绝空串与 NUL（引号转义管不了的字节）。
func idword(ident string) error {
	if ident == "" {
		return fmt.Errorf("标识符为空")
	}
	if strings.ContainsRune(ident, 0) {
		return fmt.Errorf("标识符含非法字符: %q", ident)
	}
	return nil
}

// ValidIdent 标识符合法性判定（HTTP 入口预检用）：空串/NUL 在 Quote 里是
// panic 面——那是内部不变量被破坏的信号，用户可控的表名/schema 必须在
// API 层先过本判定转 400，而不是打进 500。
func ValidIdent(ident string) bool { return idword(ident) == nil }
