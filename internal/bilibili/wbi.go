package bilibili

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
)

// wbiMixinKeyEncTab WBI 密钥重排表（bilibili-API-collect 文档口径，固定值）。
var wbiMixinKeyEncTab = [64]int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35, 27, 43, 5, 49,
	33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13, 37, 48, 7, 16, 24, 55, 40,
	61, 26, 17, 0, 1, 60, 51, 30, 4, 22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11,
	36, 20, 34, 44, 52,
}

// getMixinKey img_key+sub_key 按重排表取前 32 字符得混淆密钥。
// 上游返回异常短 key 时不足 32 位，有多少用多少（签名会失败，但不能越界 panic）。
func getMixinKey(imgKey, subKey string) string {
	s := imgKey + subKey
	var b strings.Builder
	for _, i := range wbiMixinKeyEncTab {
		if i < len(s) {
			b.WriteByte(s[i])
		}
	}
	out := b.String()
	if len(out) > 32 {
		return out[:32]
	}
	return out
}

// wbiFilterChars 参与签名的值需剔除的字符（文档口径）。
const wbiFilterChars = "!'()*"

// signWBIParams 就地补 wts 并追加 w_rid 签名参数：值过滤特殊字符 → 按 key
// 字典序 → urlencode（空格 %20，对齐 JS encodeURIComponent）→ md5(query+mixin)。
func signWBIParams(params url.Values, imgKey, subKey string, wts int64) {
	params.Set("wts", strconv.FormatInt(wts, 10))
	// 过滤值内特殊字符（url.Values 会原样编码它们，JS 参考实现不编码）
	for _, vs := range params {
		for i, v := range vs {
			if strings.ContainsAny(v, wbiFilterChars) {
				vs[i] = strings.Map(func(r rune) rune {
					if strings.ContainsRune(wbiFilterChars, r) {
						return -1
					}
					return r
				}, v)
			}
		}
	}
	query := params.Encode()
	query = strings.ReplaceAll(query, "+", "%20") // Go 编码空格为 +，文档口径是 %20
	sum := md5.Sum([]byte(query + getMixinKey(imgKey, subKey)))
	params.Set("w_rid", hex.EncodeToString(sum[:]))
}
