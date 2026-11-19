// Package houses 实现房屋租赁合同 docx 生成：内置标准模板（本仓库自产的 XML，
// {{占位符}} 保证整体处于单个 w:r run 内不被 Word 拆分）或用户上传的自定义
// 模板（完整 docx，仅替换 word/document.xml 内容，其余部件原样保留以保住
// 样式/页眉/图片）。运行时单趟替换占位符并在文末自动追加一页「房屋物品交接
// 清单」（按房屋勾选的家具家电生成），组装 docx 返回。零第三方依赖
// （2026-11-04 房屋管理批：模板可配置 + 物品清单附件页）。
package houses

import (
	"archive/zip"
	"bytes"
	"embed"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lz-monitor/internal/server/store"
)

//go:embed contract_document.xml contract_styles.xml
var tmplFS embed.FS

// docx 包骨件：内置模板的类型声明/关系文件在 Go 侧常量内联，正文与样式走 embed。
const (
	contentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
</Types>`

	rootRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

	docRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`
)

// BuildContract 按模板生成房屋租赁合同 docx（customDoc 为用户上传的完整
// docx 字节，nil = 内置标准模板）。出租方取房屋上的甲方信息，承租方取租约，
// 物品清单从房屋条目现拼（勾选加入清单且未移除的）。替换用 strings.Replacer
// 单趟完成（插入内容不会被再次扫描，用户数据里即使恰好含 {{xxx}} 字样也不会
// 被二次替换）；替换后仍残留 {{ 视为模板/数据错配，直接报错不吐脏文件。
func BuildContract(l *store.Lease, h *store.House, items []store.HouseItem, customDoc []byte) ([]byte, error) {
	if l == nil || h == nil {
		return nil, fmt.Errorf("租约或房屋不存在")
	}
	var entries []zipEntry
	var docXML string
	if len(customDoc) > 0 {
		var err error
		entries, docXML, err = loadCustomTemplate(customDoc)
		if err != nil {
			return nil, err
		}
	} else {
		styles, err := tmplFS.ReadFile("contract_styles.xml")
		if err != nil {
			return nil, fmt.Errorf("读取样式模板失败: %w", err)
		}
		doc, err := tmplFS.ReadFile("contract_document.xml")
		if err != nil {
			return nil, fmt.Errorf("读取合同模板失败: %w", err)
		}
		docXML = string(doc)
		entries = []zipEntry{
			{"[Content_Types].xml", contentTypesXML},
			{"_rels/.rels", rootRelsXML},
			{"word/_rels/document.xml.rels", docRelsXML},
			{"word/styles.xml", string(styles)},
		}
	}

	// Word 编辑过的模板会把 {{占位符}} 拆进多个 w:r run——先按段落合并修复
	//（只动含 {{ 的段落），再做单趟占位符替换
	docXML = mergeRunsForPlaceholders(docXML)
	data := contractData(l, h, items)
	pairs := make([]string, 0, len(data)*2)
	for k, v := range data {
		pairs = append(pairs, "{{"+k+"}}", xmlRun(v))
	}
	docXML = strings.NewReplacer(pairs...).Replace(docXML)
	if strings.Contains(docXML, "{{") {
		return nil, fmt.Errorf("合同模板存在未替换占位符（{{...}} 需完整写在同一段落内），模板与数据字段错配")
	}

	// 文末自动追加一页「房屋物品交接清单」（按勾选的家具家电生成，自定义模板
	// 无需写任何占位符）；插在 sectPr 之前保持页面设置仍为文档最后一段
	docXML = appendInventoryPage(docXML, l, h, items)

	entries = append(entries, zipEntry{"word/document.xml", docXML})
	return buildDocx(entries)
}

// resolveOwner 甲方（出租方）解析：2026-11-05 起随租约填写；租约未填时回退
// 房屋上的存量甲方信息（旧数据兼容）。
func resolveOwner(l *store.Lease, h *store.House) (name, idNo, phone string) {
	if l.OwnerName == "" && l.OwnerIDNo == "" && l.OwnerPhone == "" {
		return h.OwnerName, h.OwnerIDNo, h.OwnerPhone
	}
	return l.OwnerName, l.OwnerIDNo, l.OwnerPhone
}

// contractData 合同字段映射（家具电器清单保留为可用占位符：自定义模板仍可
// 内联引用；内置模板已改为引用附件清单页）。
func contractData(l *store.Lease, h *store.House, items []store.HouseItem) map[string]string {
	ownerName, ownerIDNo, ownerPhone := resolveOwner(l, h)
	return map[string]string{
		"甲方姓名":   ownerName,
		"甲方身份证号": ownerIDNo,
		"甲方电话":   ownerPhone,
		"乙方姓名":   l.TenantName,
		"乙方身份证号": l.TenantIDNo,
		"乙方电话":   l.TenantPhone,
		"房屋地址":   h.Address,
		"建筑面积":   h.Area,
		"户型":     h.Layout,
		"租期开始":   contractDate(l.StartDate),
		"租期结束":   contractDate(l.EndDate),
		"月租金":    formatMoney(l.MonthlyRent),
		"押金":     formatMoney(l.Deposit),
		"付款方式":   l.PaymentCycle,
		"家具电器清单": inventoryLines(items),
		"水卡号":    h.WaterCard,
		"电卡号":    h.ElectricCard,
		"燃气卡号":   h.GasCard,
		"其他约定":   l.Notes,
		"签订日期":   time.Now().Format("2006年1月2日"),
	}
}

// zipEntry docx 包内一个部件。
type zipEntry struct{ name, body string }

// tplEntryMax 单部件解压上限：5MB 压缩的合法模板解压后几十 MB 封顶，
// 高压缩比 zip 炸弹在此被拒（防解压打爆内存）。
const tplEntryMax = 32 << 20

// loadCustomTemplate 解用户上传的 docx：取出 word/document.xml 文本，其余部件
//（styles/页眉页脚/编号/媒体/rels）原样保留，重打包时逐字拷贝——自定义模板
// 的样式与图片引用不丢。单部件解压超限视为坏模板拒绝（zip 炸弹守卫）。
func loadCustomTemplate(doc []byte) ([]zipEntry, string, error) {
	zr, err := zip.NewReader(bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		return nil, "", fmt.Errorf("合同模板不是合法的 docx（zip）文件: %w", err)
	}
	var entries []zipEntry
	var docXML string
	found := false
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, "", fmt.Errorf("读取模板部件 %s 失败: %w", f.Name, err)
		}
		body, err := io.ReadAll(io.LimitReader(rc, tplEntryMax+1))
		rc.Close()
		if err != nil {
			return nil, "", fmt.Errorf("读取模板部件 %s 失败: %w", f.Name, err)
		}
		if len(body) > tplEntryMax {
			return nil, "", fmt.Errorf("模板部件 %s 解压后超过 32MB，疑似异常文件", f.Name)
		}
		if f.Name == "word/document.xml" {
			found = true
			docXML = string(body)
			continue
		}
		entries = append(entries, zipEntry{f.Name, string(body)})
	}
	if !found {
		return nil, "", fmt.Errorf("模板缺少 word/document.xml，请上传 Word 文档（.docx）格式")
	}
	return entries, docXML, nil
}

// buildDocx 把各部件组装为 docx（zip）。
func buildDocx(entries []zipEntry) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range entries {
		w, err := zw.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- 拆 run 修复：Word 会把 {{占位符}} 切进多个 w:r run（输入法/拼写检查/
// 二次编辑），逐段合并含 {{ 的段落文本再统一替换 ----

var (
	paraRe = regexp.MustCompile(`(?s)<w:p\b[^>]*>.*?</w:p>`)
	pPrRe  = regexp.MustCompile(`(?s)<w:pPr>.*?</w:pPr>`)
	runRe  = regexp.MustCompile(`(?s)<w:r\b[^>]*>.*?</w:r>`)
	wtRe   = regexp.MustCompile(`(?s)<w:t\b[^>]*>(.*?)</w:t>`)
	rPrRe  = regexp.MustCompile(`(?s)<w:rPr>.*?</w:rPr>`)
)

// mergeRunsForPlaceholders 对每个含 {{ 的段落：拼接全部 w:t 文本，整段重建为
// 单个 run（沿用段落属性 pPr 与首个 run 的字符属性 rPr）——段落内局部加粗等
// 字符级样式在该段让位于占位符可替换性，段落级对齐/缩进保留。
func mergeRunsForPlaceholders(doc string) string {
	return paraRe.ReplaceAllStringFunc(doc, func(p string) string {
		text := ""
		for _, m := range wtRe.FindAllStringSubmatch(p, -1) {
			text += m[1]
		}
		if !strings.Contains(text, "{{") {
			return p
		}
		open := p[:strings.Index(p, ">")+1]
		pPr := pPrRe.FindString(p)
		rPr := ""
		if runs := runRe.FindAllString(p, -1); len(runs) > 0 {
			rPr = rPrRe.FindString(runs[0])
		}
		run := "<w:r>" + rPr + `<w:t xml:space="preserve">` + text + "</w:t></w:r>"
		return open + pPr + run + "</w:p>"
	})
}

// ---- 物品交接清单附件页 ----

// appendInventoryPage 在文档 body 末尾（sectPr 之前）追加一页物品清单：勾选
// 加入清单且未移除的家具家电逐条列出。
func appendInventoryPage(doc string, l *store.Lease, h *store.House, items []store.HouseItem) string {
	var b strings.Builder
	b.WriteString(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>`)
	b.WriteString(`<w:p><w:pPr><w:jc w:val="center"/><w:spacing w:after="240"/></w:pPr><w:r><w:rPr><w:b/><w:sz w:val="32"/><w:szCs w:val="32"/></w:rPr><w:t>房屋物品交接清单</w:t></w:r></w:p>`)
	b.WriteString(para("房屋名称：" + xmlEscape(h.Name)))
	b.WriteString(para("房屋地址：" + xmlEscape(h.Address)))
	ownerName, _, _ := resolveOwner(l, h)
	b.WriteString(para("出租方（甲方）：" + xmlEscape(ownerName) + "　　承租方（乙方）：" + xmlEscape(l.TenantName)))
	b.WriteString(para("交接日期：" + xmlEscape(time.Now().Format("2006年1月2日"))))
	b.WriteString(`<w:p><w:pPr><w:jc w:val="both"/></w:pPr><w:r><w:rPr><w:b/></w:rPr><w:t>物品清单</w:t></w:r></w:p>`)
	inv := false
	for _, i := range items {
		if !i.InContract || i.Status == "removed" {
			continue
		}
		b.WriteString(para("· " + itemLine(i)))
		inv = true
	}
	if !inv {
		b.WriteString(para("无"))
	}
	b.WriteString(para("以上物品经甲乙双方当面清点确认，随房屋一并交付乙方使用；租赁期内人为损坏或缺失，由乙方照价赔偿。"))
	b.WriteString(`<w:p><w:pPr><w:spacing w:before="240"/></w:pPr><w:r><w:t xml:space="preserve">甲方（签字）：____________　　　　乙方（签字）：____________</w:t></w:r></w:p>`)
	page := b.String()
	// 插入点取最后一个 <w:sectPr（body 级页面设置）：文中分节符的 sectPr 藏在
	// 段落 pPr 内且位置靠前，取第一个会把清单页插进文档中间
	if i := strings.LastIndex(doc, "<w:sectPr"); i >= 0 {
		return doc[:i] + page + doc[i:]
	}
	return strings.Replace(doc, "</w:body>", page+"</w:body>", 1)
}

// para 清单页普通段落。
func para(text string) string {
	return `<w:p><w:pPr><w:jc w:val="both"/><w:ind w:firstLineChars="200" w:firstLine="420"/></w:pPr><w:r><w:t xml:space="preserve">` + text + "</w:t></w:r></w:p>"
}

// itemLine 单条物品文案（清单页与内联清单共用）：名称（品牌型号）×数量〔损坏〕。
func itemLine(i store.HouseItem) string {
	cat := "家具"
	if i.Category == "appliance" {
		cat = "家电"
	}
	line := cat + "：" + i.Name
	if i.BrandModel != "" {
		line += "（" + i.BrandModel + "）"
	}
	if i.Qty > 1 {
		line += "×" + strconv.FormatInt(i.Qty, 10)
	}
	if i.Status == "broken" {
		line += "〔损坏〕"
	}
	return line
}

// inventoryLines 内联清单文案（{{家具电器清单}} 占位符用，自定义模板可引用）：
// 已移除与未勾选加入清单的不列，无条目显示「无」。
func inventoryLines(items []store.HouseItem) string {
	var lines []string
	for _, i := range items {
		if !i.InContract || i.Status == "removed" {
			continue
		}
		lines = append(lines, "· "+itemLine(i))
	}
	if len(lines) == 0 {
		return "无"
	}
	return strings.Join(lines, "\n")
}

// xmlRun 值做 XML 转义并把换行转成 <w:br/>（docx 内软换行，run 级样式对其余
// 文本仍然生效）。
func xmlRun(v string) string {
	esc := xmlEscape(v)
	return strings.ReplaceAll(esc, "\n", `</w:t><w:br/><w:t xml:space="preserve">`)
}

// xmlEscape OOXML 文本节点转义。
func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

// formatMoney 金额展示：去尾零（3500.00 → 3500、3500.50 → 3500.5）。
func formatMoney(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// contractDate YYYY-MM-DD → 「2006年1月2日」（解析失败原样返回，兜底不炸）。
func contractDate(s string) string {
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return s
	}
	return t.Format("2006年1月2日")
}
