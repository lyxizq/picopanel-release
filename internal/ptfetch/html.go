package ptfetch

import (
	"bytes"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// 极简 DOM 工具（golang.org/x/net/html）：NexusPHP 页面解析只需要
// 「按标签收集」「按文本找元素」「相邻元素」「取属性」「全文文本」这几个原语。

// parseHTML 解析文档并转 UTF-8：先按声明的 charset（含 meta 嗅探，个别老站
// 仍是 GBK 系）解码，解析失败原样按 UTF-8 容错。
func parseHTML(body string) *html.Node {
	data := []byte(body)
	enc, name, certain := charset.DetermineEncoding(data, "")
	// 未声明编码时 DetermineEncoding 回落 windows-1252，勿盲信：内容本身是
	// 合法 UTF-8 就直接用，只对「声明了非 UTF-8 且字节确实非 UTF-8」的老站解码
	if !(certain && strings.EqualFold(name, "utf-8")) && !utf8.Valid(data) {
		if b, err := io.ReadAll(enc.NewDecoder().Reader(bytes.NewReader(data))); err == nil {
			data = b
		}
	}
	root, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return root
}

// eachTag 深度优先收集指定标签的全部元素。
func eachTag(root *html.Node, tag string) []*html.Node {
	var out []*html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == tag {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// textOf 元素内全部文本（递归，空白折叠）。
func textOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textOf(c))
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// attrOf 元素属性值（无则空串）。
func attrOf(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

// nextElem 下一个元素兄弟节点（跳过文本/注释）。
func nextElem(n *html.Node) *html.Node {
	for c := n.NextSibling; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			return c
		}
	}
	return nil
}

// childElems 直接子级里的指定标签元素（不递归）。
func childElems(n *html.Node, tag string) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == tag {
			out = append(out, c)
		}
	}
	return out
}

// findById 按 id 找元素（TTG 的做种容器 div#ka2）。
func findById(root *html.Node, id string) *html.Node {
	if root == nil {
		return nil
	}
	var found *html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n == nil || found != nil {
			return
		}
		if n.Type == html.ElementNode && attrOf(n, "id") == id {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}

// findLinks 收集 href 含 substr 的 <a>（顺序即文档序）。
func findLinks(root *html.Node, substr string) []*html.Node {
	var out []*html.Node
	for _, a := range eachTag(root, "a") {
		if strings.Contains(attrOf(a, "href"), substr) {
			out = append(out, a)
		}
	}
	return out
}

// findRowExact 找「自身文本去尾冒号后等于 label」的 <td>（NexusPHP userdetails
// 的行头写法：td.rowhead 文本恰为「魔力值:」），值取相邻的下一个 <td>。
func findRowExact(root *html.Node, labels ...string) *html.Node {
	for _, td := range eachTag(root, "td") {
		t := strings.TrimRight(strings.TrimSpace(textOf(td)), ":：")
		for _, l := range labels {
			if t == l {
				return td
			}
		}
	}
	return nil
}

// findRowContains 找「自身文本包含 label」的 <td>（行头带图标/前后缀的站用，
// exact 落空后的兜底；调用方须只传不会误伤邻行的特称标签）。
func findRowContains(root *html.Node, labels ...string) *html.Node {
	for _, td := range eachTag(root, "td") {
		t := textOf(td)
		for _, l := range labels {
			if l != "" && strings.Contains(t, l) {
				return td
			}
		}
	}
	return nil
}

// rowHeadTags 行头容器标签序（456）：经典 NexusPHP 是 td，改版站见 span
//（憨憨新版 span+span/div 行）与 div（audiences 内联块）；扩容器扫描按此
// 序进行，经典站的 td 命中优先不受影响。
var rowHeadTags = [...]string{"td", "span", "div"}

// findRowAny findRowExact 的扩容器版：td→span→div 三轮、轮内按文档序，自文
// 本去尾冒号恰等 label 即命中；值仍走 rowValue（nextElem 对相邻 span/div 同
// 样适用）。
func findRowAny(root *html.Node, labels ...string) *html.Node {
	for _, tag := range rowHeadTags {
		for _, el := range eachTag(root, tag) {
			t := strings.TrimRight(strings.TrimSpace(textOf(el)), ":：")
			for _, l := range labels {
				if t == l {
					return el
				}
			}
		}
	}
	return nil
}

// findRowAnyContains findRowContains 的扩容器版：td→span→div 三轮；轮内取
// 「文本最短」的命中（最具体的容器优先，防外层大容器抢在真正行头前面）。
func findRowAnyContains(root *html.Node, labels ...string) *html.Node {
	for _, tag := range rowHeadTags {
		var hit *html.Node
		hitLen := 0
		for _, el := range eachTag(root, tag) {
			t := textOf(el)
			for _, l := range labels {
				if l != "" && strings.Contains(t, l) {
					if hit == nil || len(t) < hitLen {
						hit, hitLen = el, len(t)
					}
					break
				}
			}
		}
		if hit != nil {
			return hit
		}
	}
	return nil
}

// labelInline 同格内联形态兜底（456）：「U币: 1,234」「加入日期：2022-09-22」
// 标签与值挤在同一容器（ubits rowfollow 行 / audiences div 块）——找含
// 「label + 0-2 字符内冒号」的最短元素，返回冒号后的文本。
func labelInline(root *html.Node, labels ...string) string {
	best, bestLen := "", 0
	for _, tag := range rowHeadTags {
		for _, el := range eachTag(root, tag) {
			t := textOf(el)
			for _, l := range labels {
				if l == "" {
					continue
				}
				i := strings.Index(t, l)
				if i < 0 {
					continue
				}
				rest := t[i+len(l):]
				if c := strings.IndexAny(rest, ":："); c >= 0 && c <= 2 {
					if v := strings.TrimSpace(rest[c+1:]); v != "" && (best == "" || len(t) < bestLen) {
						best, bestLen = v, len(t)
					}
					break
				}
			}
		}
	}
	return best
}

// findByClass 找 class 属性含子串的首个元素（audiences 新版 userbar 等
// 改版站的类名锚点，取到后 textOf 读值）。
func findByClass(root *html.Node, substr string) *html.Node {
	if root == nil {
		return nil
	}
	var found *html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n == nil || found != nil {
			return
		}
		if n.Type == html.ElementNode && strings.Contains(attrOf(n, "class"), substr) {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}

// rowValue 行头的下一个 <td> 文本。
func rowValue(row *html.Node) string {
	if row == nil {
		return ""
	}
	return textOf(nextElem(row))
}

// imgTitleOf 取元素内第一个 <img> 的 title/alt 属性（NexusPHP 等级行是图片徽章）。
func imgTitleOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	for _, img := range eachTag(n, "img") {
		if t := attrOf(img, "title"); t != "" {
			return t
		}
		if t := attrOf(img, "alt"); t != "" {
			return t
		}
	}
	return ""
}

// levelTextOf 等级值三形态（484）：①<img title/alt> 徽章（经典 NexusPHP）；
// ②<b title> 属性（pttime 等级名挂属性、格内无文本）；③整格纯文本。
func levelTextOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	if t := imgTitleOf(n); t != "" {
		return t
	}
	for _, b := range eachTag(n, "b") {
		if t := strings.TrimSpace(attrOf(b, "title")); t != "" {
			return t
		}
	}
	return strings.TrimSpace(textOf(n))
}

// classBLevel audiences ud-class 元素里抠等级名（484）：b 文本优先（站方
// 展示名），退 b 的 class「XXX_Name」形态（PT-depiler 口径，下划线转空格）。
func classBLevel(el *html.Node) string {
	for _, b := range eachTag(el, "b") {
		if t := strings.TrimSpace(textOf(b)); t != "" {
			return t
		}
		if m := reLevelClass.FindStringSubmatch(attrOf(b, "class")); m != nil {
			return strings.ReplaceAll(m[1], "_", " ")
		}
	}
	return strings.TrimSpace(textOf(el))
}
