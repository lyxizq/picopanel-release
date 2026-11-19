package houses

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"lz-monitor/internal/server/store"
)

// unzipEntry 解 docx 取指定部件文本。
func unzipEntry(t *testing.T, data []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("产物不是合法 zip/docx: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	t.Fatalf("docx 缺部件 %s", name)
	return ""
}

// zipEntries docx 内全部部件名。
func zipEntries(t *testing.T, data []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	return out
}

func sampleData() (*store.Lease, *store.House, []store.HouseItem) {
	l := &store.Lease{
		ID: 1, HouseID: 2, Status: "active", TenantName: "张三", TenantPhone: "13800000000",
		TenantIDNo: "110101199001010011", StartDate: "2026-01-01", EndDate: "2026-12-31",
		MonthlyRent: 3500.5, Deposit: 7000, PaymentCycle: "押一付三", Notes: "水电燃气由租客承担",
	}
	h := &store.House{
		ID: 2, Name: "望江府 1201", Address: "江城 A&B 街 1 号", Area: "89㎡", Layout: "两室一厅",
		OwnerName: "李房东", OwnerPhone: "13900000000", OwnerIDNo: "110101197001010022",
		WaterCard: "W-001", ElectricCard: "E-001", GasCard: "G-001",
	}
	items := []store.HouseItem{
		{ID: 1, HouseID: 2, Category: "appliance", Name: "空调", BrandModel: "格力 KFR-35", Qty: 2, Status: "active", InContract: true},
		{ID: 2, HouseID: 2, Category: "furniture", Name: "旧床垫", Qty: 1, Status: "broken", InContract: true},
		{ID: 3, HouseID: 2, Category: "furniture", Name: "不进清单的桌子", Qty: 1, Status: "active", InContract: false},
		{ID: 4, HouseID: 2, Category: "furniture", Name: "已搬走的桌子", Qty: 1, Status: "removed", InContract: true},
	}
	return l, h, items
}

// TestBuildContract 合同 docx 生成（2026-11-04 版）：占位符零残留、字段值落位、
// XML 转义、文末自动追加「房屋物品交接清单」页（勾选过滤：in_contract=false
// 与已移除不进清单）、整体是合法 XML。
func TestBuildContract(t *testing.T) {
	l, h, items := sampleData()
	data, err := BuildContract(l, h, items, nil)
	if err != nil {
		t.Fatalf("生成合同失败: %v", err)
	}
	doc := unzipEntry(t, data, "word/document.xml")

	if strings.Contains(doc, "{{") {
		t.Fatal("替换后仍残留占位符")
	}
	for _, want := range []string{
		"张三", "13800000000", "110101199001010011", // 乙方
		"李房东", "13900000000", // 甲方
		"江城 A&amp;B 街 1 号", // 房屋坐落（合同以地址为准；& 已转义）
		"89㎡", "两室一厅",
		"2026年1月1日", "2026年12月31日", // 租期中文化
		"3500.5", "7000", "押一付三", // 金额去尾零
		"W-001", "E-001", "G-001", // 三卡号
		"水电燃气由租客承担", // 其他约定
		"房屋租赁合同",
		"房屋物品交接清单", // 附件页标题
		"交接日期：", "甲方（签字）：____________",
		"家电：空调（格力 KFR-35）×2", // 清单页：数量>1 显 ×N
		"家具：旧床垫〔损坏〕",          // 损坏标注
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("合同缺字段值 %q", want)
		}
	}
	for _, banned := range []string{"不进清单的桌子", "已搬走的桌子"} {
		if strings.Contains(doc, banned) {
			t.Fatalf("%q 不应进清单（未勾选/已移除）", banned)
		}
	}
	if strings.Contains(doc, "A&B") {
		t.Fatal("原始 & 未转义（应为 A&amp;B）")
	}
	if !strings.Contains(doc, `<w:br w:type="page"/>`) {
		t.Fatal("清单页应分页开始")
	}
	if !strings.Contains(doc, "乙方（签字）：____________</w:t></w:r></w:p><w:sectPr") {
		t.Fatal("清单页（末尾签字行）应插在 sectPr 之前（页面设置保持文档末尾）")
	}
	if err := xml.Unmarshal([]byte(doc), new(any)); err != nil {
		t.Fatalf("document.xml 不是合法 XML: %v", err)
	}
}

// TestBuildContractEmpty 空数据兜底：无清单显示「无」，日期解析失败原样落。
func TestBuildContractEmpty(t *testing.T) {
	l := &store.Lease{ID: 1, HouseID: 2, Status: "active", TenantName: "张三", StartDate: "bad", EndDate: "2026-12-31"}
	h := &store.House{ID: 2, Name: "毛坯房"}
	data, err := BuildContract(l, h, nil, nil)
	if err != nil {
		t.Fatalf("生成合同失败: %v", err)
	}
	doc := unzipEntry(t, data, "word/document.xml")
	if strings.Contains(doc, "{{") {
		t.Fatal("空数据仍应替换干净")
	}
	if !strings.Contains(doc, "<w:t xml:space=\"preserve\">无</w:t>") {
		t.Fatal("无家具电器应显示「无」")
	}
	if !strings.Contains(doc, "bad") {
		t.Fatal("坏日期应原样保留（不炸）")
	}
}

// TestBuildContractGuard 入参缺失与坏模板。
func TestBuildContractGuard(t *testing.T) {
	l, h, items := sampleData()
	if _, err := BuildContract(nil, h, items, nil); err == nil {
		t.Fatal("缺租约应报错")
	}
	if _, err := BuildContract(l, nil, items, nil); err == nil {
		t.Fatal("缺房屋应报错")
	}
	if _, err := BuildContract(l, h, items, []byte("not a zip")); err == nil {
		t.Fatal("非 zip 的自定义模板应报错")
	}
	bad := buildTestDocx(t, map[string]string{"word/styles.xml": "x"}) // 缺 document.xml
	if _, err := BuildContract(l, h, items, bad); err == nil {
		t.Fatal("缺 document.xml 的模板应报错")
	}
}

// buildTestDocx 内存组装最小 docx（测试自定义模板用）。
func buildTestDocx(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestBuildContractCustom 自定义模板（2026-11-04）：Word 编辑会把 {{占位符}}
// 拆进多个 w:r run——拆 run 修复后应正常替换；其余部件（styles 等）逐字保留；
// 物品清单页自动追加且 sectPr 保持文档末尾。
func TestBuildContractCustom(t *testing.T) {
	l, h, items := sampleData()
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:p><w:pPr><w:jc w:val="center"/></w:pPr><w:r><w:rPr><w:b/></w:rPr><w:t>租房合同（甲方{{甲方` +
		`</w:t></w:r><w:r><w:t>姓名}}，乙方{{乙方姓名}}）</w:t></w:r></w:p>` +
		`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`
	custom := buildTestDocx(t, map[string]string{
		"[Content_Types].xml": `<Types/>`,
		"word/styles.xml":     `<w:styles>KEEP_ME</w:styles>`,
		"word/document.xml":   docXML,
	})
	data, err := BuildContract(l, h, items, custom)
	if err != nil {
		t.Fatalf("自定义模板生成失败: %v", err)
	}
	out := unzipEntry(t, data, "word/document.xml")
	if strings.Contains(out, "{{") {
		t.Fatalf("拆 run 占位符未修复替换: %s", out)
	}
	if !strings.Contains(out, "租房合同（甲方李房东，乙方张三）") {
		t.Fatalf("自定义模板字段未落位: %s", out)
	}
	if !strings.Contains(out, "房屋物品交接清单") {
		t.Fatal("自定义模板也应自动追加物品清单页")
	}
	if !strings.Contains(out, "乙方（签字）：____________</w:t></w:r></w:p><w:sectPr") {
		t.Fatal("清单页（末尾签字行）应插在自定义模板的 sectPr 之前")
	}
	// 其余部件逐字保留（样式/图件不丢）
	if got := unzipEntry(t, data, "word/styles.xml"); got != `<w:styles>KEEP_ME</w:styles>` {
		t.Fatalf("styles.xml 应原样保留: %q", got)
	}
	if got := unzipEntry(t, data, "[Content_Types].xml"); got != `<Types/>` {
		t.Fatalf("[Content_Types].xml 应原样保留: %q", got)
	}
	names := zipEntries(t, data)
	if len(names) != 3 {
		t.Fatalf("部件数应保留 2 + document.xml = 3: %v", names)
	}
}

// TestBuildContractMidDocSectPr 文中分节符（2026-11-05）：自定义模板的段落
// pPr 内可嵌 sectPr（Word 分节符），清单页必须插在最后一个（body 级）sectPr
// 之前——插第一个会把清单页塞进文档中间。
func TestBuildContractMidDocSectPr(t *testing.T) {
	l, h, items := sampleData()
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:p><w:pPr><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:pPr><w:r><w:t>上半节</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>下半节{{乙方姓名}}</w:t></w:r></w:p>` +
		`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`
	custom := buildTestDocx(t, map[string]string{"word/document.xml": docXML})
	data, err := BuildContract(l, h, items, custom)
	if err != nil {
		t.Fatalf("含文中分节符的模板生成失败: %v", err)
	}
	out := unzipEntry(t, data, "word/document.xml")
	if !strings.Contains(out, "下半节张三") {
		t.Fatalf("占位符未替换: %s", out)
	}
	if !strings.Contains(out, "乙方（签字）：____________</w:t></w:r></w:p><w:sectPr") {
		t.Fatal("清单页应插在最后一个（body 级）sectPr 之前")
	}
	if strings.Contains(out, "房屋物品交接清单</w:t></w:r></w:p></w:p>") {
		t.Fatal("清单页不应插在文中分节符段落处")
	}
	// 文中分节符的段落仍完整保留在上半部分
	if !strings.Contains(out, "上半节") {
		t.Fatal("正文内容不应丢失")
	}
}

// TestBuildContractOwnerSource 甲方来源（2026-11-05）：出租方随租约后，租约
// 甲方优先；租约未填时回退房屋上的存量甲方信息（旧数据兼容）。
func TestBuildContractOwnerSource(t *testing.T) {
	l, h, items := sampleData()
	l2 := *l
	l2.OwnerName, l2.OwnerIDNo, l2.OwnerPhone = "约上房东", "110101198001010033", "13700000000"
	data, err := BuildContract(&l2, h, items, nil)
	if err != nil {
		t.Fatal(err)
	}
	doc := unzipEntry(t, data, "word/document.xml")
	if !strings.Contains(doc, "约上房东") || !strings.Contains(doc, "13700000000") {
		t.Fatal("租约甲方应优先")
	}
	if strings.Contains(doc, "李房东") {
		t.Fatal("租约已填甲方时不应再取房屋存量")
	}
	// 回退：租约甲方全空 → 用房屋存量
	data, err = BuildContract(l, h, items, nil)
	if err != nil {
		t.Fatal(err)
	}
	doc = unzipEntry(t, data, "word/document.xml")
	if !strings.Contains(doc, "李房东") {
		t.Fatal("租约甲方为空应回退房屋存量")
	}
}
