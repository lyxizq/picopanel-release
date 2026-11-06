package mail

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"regexp"
	"strings"

	"github.com/emersion/go-message"
	// go-message 默认不带字符集表（gbk 等会报 unknown charset 且 Body 为空），
	// 空导入官方扩展注册全局 CharsetReader（ianaindex/htmlindex，覆盖 gbk/big5 等）
	_ "github.com/emersion/go-message/charset"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

var tagRe = regexp.MustCompile(`<[^>]*>`)

// ParseMIME 解析整封报文（316/318）：正文取 text/html（缺失剥 text/plain 标签兜底），
// 附件按 DFS 序编号（Idx 1 起）并记 IMAP part 路径（下载按此定位）。
// charset 兼容 gbk/gb2312/big5（x/text）；RFC2047 编码头自解（mime.WordDecoder
// 挂 x/text 的 charset reader，gbk 编码词也认）。
func ParseMIME(raw []byte) *Message {
	m := &Message{Attachments: []Attachment{}}
	root, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		m.Text = decodeCharset("", raw)
		return m
	}
	m.Subject = decodeWords(root.Header.Get("Subject"))
	if v := root.Header.Get("From"); v != "" {
		m.FromName, m.FromAddr = headerAddr(v)
	}
	if v := root.Header.Get("To"); v != "" {
		m.To = decodeWords(v)
	}
	if v := root.Header.Get("Cc"); v != "" {
		m.Cc = decodeWords(v)
	}
	m.MessageID = strings.Trim(root.Header.Get("Message-ID"), "<> ")
	if d, err := mail.ParseDate(root.Header.Get("Date")); err == nil {
		m.DateUnix = d.Unix()
	}

	var textPlain, textHTML string
	attIdx := 0
	_ = root.Walk(func(path []int, e *message.Entity, werr error) error {
		if werr != nil { // 该部件解码失败：跳过（charset 扩展已覆盖常见集）
			return nil
		}
		ct, params, _ := e.Header.ContentType()
		ct = strings.ToLower(strings.TrimSpace(ct))
		// multipart 容器节点：绝不读 Body（读了会毁掉 Walk 的后续遍历），只下钻
		if strings.HasPrefix(ct, "multipart/") {
			return nil
		}
		disp, dparams, _ := e.Header.ContentDisposition()
		isAttachment := strings.EqualFold(disp, "attachment") || dparams["filename"] != ""
		// go-message 路径 0 基、IMAP part 编号 1 基：逐段 +1 对齐。
		// 非 multipart 单部件邮件（通知类最常见）正文就在根节点 path=[]，
		// IMAP 口径其 part 路径为 "1"（BODY[1]）——跳过根节点=整封空白（事故）
		var ppath []string
		if len(path) == 0 {
			ppath = []string{"1"}
		} else {
			for _, n := range path {
				ppath = append(ppath, fmt.Sprintf("%d", n+1))
			}
		}

		if isAttachment || (ct != "text/plain" && ct != "text/html" && (params["name"] != "" || hasName(e))) {
			attIdx++
			name := dparams["filename"]
			if name == "" {
				name = params["name"]
			}
			if name == "" {
				name = fmt.Sprintf("attachment-%d", attIdx)
			}
			size := int64(0)
			if b, rerr := io.ReadAll(io.LimitReader(e.Body, maxMessageLen)); rerr == nil {
				size = int64(len(b))
			}
			m.Attachments = append(m.Attachments, Attachment{
				Idx: attIdx, Path: strings.Join(ppath, "."), Name: name, CT: ct, Size: size,
			})
			return nil
		}
		body, rerr := io.ReadAll(io.LimitReader(e.Body, maxMessageLen))
		if rerr != nil {
			return nil
		}
		// charset 扩展已把已知字符集解成 UTF-8；仅当扩展不认（werr 非空）才自己解
		content := string(body)
		if werr != nil {
			content = decodeCharset(params["charset"], body)
		}
		content = strings.TrimRight(content, "\r\n") // 去掉信体尾部换行（展示干净）
		switch ct {
		case "text/html":
			textHTML = content
		case "text/plain":
			textPlain = content
		case "": // 无 Content-Type 的裸叶子按纯文本
			if textPlain == "" && textHTML == "" {
				textPlain = content
			}
		}
		return nil
	})

	m.HTML = textHTML
	m.Text = textPlain
	if m.Text == "" && m.HTML != "" {
		m.Text = tagRe.ReplaceAllString(m.HTML, " ")
	}
	return m
}

func hasName(e *message.Entity) bool {
	_, params, err := e.Header.ContentType()
	return err == nil && params["name"] != ""
}

// ExtractPart 从整封报文按 IMAP part 路径抽取附件字节（318 下载）。
func ExtractPart(raw []byte, partPath string) (name, ct string, data []byte, err error) {
	root, rerr := message.Read(bytes.NewReader(raw))
	if rerr != nil {
		return "", "", nil, rerr
	}
	var path []int
	for _, seg := range strings.Split(partPath, ".") {
		var n int
		if _, err := fmt.Sscanf(seg, "%d", &n); err != nil {
			return "", "", nil, fmt.Errorf("非法附件路径")
		}
		path = append(path, n-1) // IMAP 1 基 → go-message 0 基；单部件邮件 part "1" → path=[0]=根
	}
	// 单部件邮件：part "1" 就是根实体本身（Walk 里根是 path=[]，不存在 [0]）
	if len(path) == 1 && path[0] == 0 {
		if ct0, _, _ := root.Header.ContentType(); !strings.HasPrefix(strings.ToLower(ct0), "multipart/") {
			root2 := root
			name0 := params0Name(root2.Header)
			data0, err := io.ReadAll(io.LimitReader(root2.Body, maxMessageLen))
			if err != nil {
				return "", "", nil, err
			}
			ct0, _, _ := root2.Header.ContentType()
			return name0, ct0, bytes.TrimRight(data0, "\r\n"), nil
		}
	}
	var found *message.Entity
	_ = root.Walk(func(p []int, e *message.Entity, _ error) error {
		if len(p) == len(path) {
			same := true
			for i := range p {
				if p[i] != path[i] {
					same = false
					break
				}
			}
			if same {
				found = e
				return io.EOF // 命中即停，避免整树读入
			}
		}
		return nil
	})
	if found == nil {
		return "", "", nil, fmt.Errorf("附件不存在（part %s）", partPath)
	}
	ctp, params, _ := found.Header.ContentType()
	ct = ctp
	name = params["name"]
	if d, dp, _ := found.Header.ContentDisposition(); strings.EqualFold(d, "attachment") && dp["filename"] != "" {
		name = dp["filename"]
	}
	if name == "" {
		name = "attachment-" + partPath
	}
	data, err = io.ReadAll(io.LimitReader(found.Body, maxMessageLen))
	if err != nil {
		return "", "", nil, err
	}
	return name, ct, data, nil
}

// params0Name 从 Header 取附件名（Content-Disposition filename 优先，回落 name 参数）。
func params0Name(h message.Header) string {
	_, dp, _ := h.ContentDisposition()
	if dp["filename"] != "" {
		return dp["filename"]
	}
	_, params, _ := h.ContentType()
	return params["name"]
}

// decodeWords 解 RFC2047 编码词（=?gbk?B?..?=），charset 挂 x/text 兼容 gbk/big5。
func decodeWords(s string) string {
	dec := &mime.WordDecoder{CharsetReader: charsetReader}
	if out, err := dec.DecodeHeader(s); err == nil {
		return out
	}
	return s
}

// headerAddr 从原始 From/To 头取首个「显示名 + 地址」。
func headerAddr(v string) (name, addr string) {
	if a, err := mail.ParseAddress(decodeWords(v)); err == nil {
		return a.Name, a.Address
	}
	// 兜底：裸地址
	if i := strings.LastIndex(v, "<"); i >= 0 && strings.HasSuffix(v, ">") {
		return decodeWords(strings.TrimSpace(v[:i])), strings.Trim(v[i:], "<>")
	}
	return "", decodeWords(v)
}

// charsetReader x/text 后端：gbk/gb2312/gb18030/big5/utf-16/iso-8859-* 兜底 latin1。
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "gbk", "gb2312", "gb18030", "cp936":
		return simplifiedchinese.GB18030.NewDecoder().Reader(input), nil
	case "big5", "big5-hkscs", "big5hkscs":
		return traditionalchinese.Big5.NewDecoder().Reader(input), nil
	case "utf-16", "utf-16le", "utf-16be":
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder().Reader(input), nil
	default: // iso-8859-1/unknown：按原样透传（net/mail 已处理 utf-8）
		return input, nil
	}
}

// decodeCharset 按 charset 参数转 UTF-8（gbk/gb2312/big5/utf-8；未知原样返回）。
func decodeCharset(charset string, data []byte) string {
	cs := strings.ToLower(strings.TrimSpace(charset))
	switch cs {
	case "", "utf-8", "utf8", "us-ascii":
		return string(data)
	case "gbk", "gb2312", "gb18030", "cp936":
		if out, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data); err == nil {
			return string(out)
		}
		return string(data)
	case "big5", "big5-hkscs":
		if out, err := traditionalchinese.Big5.NewDecoder().Bytes(data); err == nil {
			return string(out)
		}
		return string(data)
	default:
		// UTF-16 家族（极少见的纯文本场景）
		if strings.Contains(cs, "utf-16") {
			enc := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
			if out, err := enc.NewDecoder().Bytes(data); err == nil {
				return string(out)
			}
		}
		return string(data)
	}
}
