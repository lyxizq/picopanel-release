package api

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/houses"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webhook"
	"lz-monitor/internal/server/webpush"
	"lz-monitor/internal/server/wxpusher"
)

// HouseHandler 房屋管理（2026-11-03）：房屋/家具电器/租约 CRUD + 总览统计 +
// 租赁合同 docx 下载 + 租约到期推送。管理员专属模块（不进 familyRoutes，
// 受限用户 deny-by-default 403），数据不做成员隔离。
type HouseHandler struct {
	st *store.Store
}

// NewHouseHandler 创建 handler。
func NewHouseHandler(st *store.Store) *HouseHandler { return &HouseHandler{st: st} }

// List 房屋总览：全部房屋（带在租租约联查/现算展示）+ 统计（月总租金/年总
// 租金按在租租约年化）。
func (h *HouseHandler) List(c *gin.Context) {
	list, stats, err := h.st.ListHouses()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询房屋失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": list, "stats": stats})
}

type houseReq struct {
	Name         string `json:"name" binding:"required"`
	Address      string `json:"address"`
	Area         string `json:"area"`
	Layout       string `json:"layout"`
	Status       string `json:"status"`
	OwnerName    string `json:"owner_name"`
	OwnerPhone   string `json:"owner_phone"`
	OwnerIDNo    string `json:"owner_id_no"`
	WaterCard    string `json:"water_card"`
	ElectricCard string `json:"electric_card"`
	GasCard      string `json:"gas_card"`
	RentDueDay   int    `json:"rent_due_day"`
	RentDueTime  string `json:"rent_due_time"`
	Notes        string `json:"notes"`
}

// Create 新建房屋。
func (h *HouseHandler) Create(c *gin.Context) {
	var req houseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（房屋名称必填）"})
		return
	}
	m := &store.House{Name: req.Name, Address: req.Address, Area: req.Area, Layout: req.Layout, Status: req.Status,
		OwnerName: req.OwnerName, OwnerPhone: req.OwnerPhone, OwnerIDNo: req.OwnerIDNo,
		WaterCard: req.WaterCard, ElectricCard: req.ElectricCard, GasCard: req.GasCard,
		RentDueDay: req.RentDueDay, RentDueTime: req.RentDueTime, Notes: req.Notes}
	if err := h.st.CreateHouse(m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := h.st.GetHouse(m.ID) // 回读带现算展示字段
	if err != nil {
		c.JSON(http.StatusOK, m)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Update 编辑房屋。
func (h *HouseHandler) Update(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req houseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（房屋名称必填）"})
		return
	}
	m := &store.House{ID: id, Name: req.Name, Address: req.Address, Area: req.Area, Layout: req.Layout, Status: req.Status,
		OwnerName: req.OwnerName, OwnerPhone: req.OwnerPhone, OwnerIDNo: req.OwnerIDNo,
		WaterCard: req.WaterCard, ElectricCard: req.ElectricCard, GasCard: req.GasCard,
		RentDueDay: req.RentDueDay, RentDueTime: req.RentDueTime, Notes: req.Notes}
	if err := h.st.UpdateHouse(m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := h.st.GetHouse(id)
	if err != nil {
		c.JSON(http.StatusOK, m)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Delete 删除房屋（有在租租约拒绝；家具电器与历史租约级联删除）。
func (h *HouseHandler) Remove(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.st.DeleteHouse(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Detail 房屋详情：房屋 + 家具电器 + 租约 + 消耗费用（详情右栏 tab 一次取齐）。
func (h *HouseHandler) Detail(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	house, err := h.st.GetHouse(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	items, err := h.st.ListHouseItems(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询家具电器失败"})
		return
	}
	leases, err := h.st.ListLeases(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询租约失败"})
		return
	}
	expenses, err := h.st.ListHouseExpenses(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询消耗费用失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"house": house, "items": items, "leases": leases, "expenses": expenses})
}

type houseItemReq struct {
	HouseID    int64  `json:"house_id" binding:"required"`
	Category   string `json:"category"`
	Name       string `json:"name" binding:"required"`
	BrandModel string `json:"brand_model"`
	Qty        int64  `json:"qty"`
	Status     string `json:"status"`
	InContract *bool  `json:"in_contract"` // 加入合同物品清单（缺省=勾选）
	Notes      string `json:"notes"`
}

// itemInContract 勾选缺省 true（未传 = 默认加入清单）。
func itemInContract(v *bool) bool {
	return v == nil || *v
}

// CreateItem 新建家具电器。
func (h *HouseHandler) CreateItem(c *gin.Context) {
	var req houseItemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（房屋与名称必填）"})
		return
	}
	i := &store.HouseItem{HouseID: req.HouseID, Category: req.Category, Name: req.Name, BrandModel: req.BrandModel,
		Qty: req.Qty, Status: req.Status, InContract: itemInContract(req.InContract), Notes: req.Notes}
	if err := h.st.CreateHouseItem(i); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, i)
}

// UpdateItem 编辑家具电器。
func (h *HouseHandler) UpdateItem(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req houseItemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（名称必填）"})
		return
	}
	i := &store.HouseItem{ID: id, Category: req.Category, Name: req.Name, BrandModel: req.BrandModel,
		Qty: req.Qty, Status: req.Status, InContract: itemInContract(req.InContract), Notes: req.Notes}
	if err := h.st.UpdateHouseItem(i); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, i)
}

// RemoveItem 删除家具电器。
func (h *HouseHandler) RemoveItem(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.st.DeleteHouseItem(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

type leaseReq struct {
	HouseID         int64   `json:"house_id" binding:"required"`
	TenantName      string  `json:"tenant_name" binding:"required"`
	TenantPhone     string  `json:"tenant_phone"`
	TenantIDNo      string  `json:"tenant_id_no"`
	OwnerName       string  `json:"owner_name"`
	OwnerPhone      string  `json:"owner_phone"`
	OwnerIDNo       string  `json:"owner_id_no"`
	WaterReading    float64 `json:"water_reading"`
	ElectricReading float64 `json:"electric_reading"`
	GasReading      float64 `json:"gas_reading"`
	StartDate       string  `json:"start_date" binding:"required"`
	EndDate         string  `json:"end_date" binding:"required"`
	MonthlyRent     float64 `json:"monthly_rent"`
	Deposit         float64 `json:"deposit"`
	PaymentCycle    string  `json:"payment_cycle"`
	Notes           string  `json:"notes"`
}

// CreateLease 新建租约（同房在租排他）。
func (h *HouseHandler) CreateLease(c *gin.Context) {
	var req leaseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（房屋、租客姓名与租期必填）"})
		return
	}
	l := &store.Lease{HouseID: req.HouseID, TenantName: req.TenantName, TenantPhone: req.TenantPhone, TenantIDNo: req.TenantIDNo,
		OwnerName: req.OwnerName, OwnerPhone: req.OwnerPhone, OwnerIDNo: req.OwnerIDNo,
		WaterReading: req.WaterReading, ElectricReading: req.ElectricReading, GasReading: req.GasReading,
		StartDate: req.StartDate, EndDate: req.EndDate, MonthlyRent: req.MonthlyRent, Deposit: req.Deposit,
		PaymentCycle: req.PaymentCycle, Notes: req.Notes}
	if err := h.st.CreateLease(l); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := h.st.GetLease(l.ID)
	if err != nil {
		c.JSON(http.StatusOK, l)
		return
	}
	c.JSON(http.StatusOK, out)
}

// UpdateLease 编辑租约（到期日变动自动重置 30 天提醒标记）。
func (h *HouseHandler) UpdateLease(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req leaseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（租客姓名与租期必填）"})
		return
	}
	l := &store.Lease{ID: id, TenantName: req.TenantName, TenantPhone: req.TenantPhone, TenantIDNo: req.TenantIDNo,
		OwnerName: req.OwnerName, OwnerPhone: req.OwnerPhone, OwnerIDNo: req.OwnerIDNo,
		WaterReading: req.WaterReading, ElectricReading: req.ElectricReading, GasReading: req.GasReading,
		StartDate: req.StartDate, EndDate: req.EndDate, MonthlyRent: req.MonthlyRent, Deposit: req.Deposit,
		PaymentCycle: req.PaymentCycle, Notes: req.Notes}
	if err := h.st.UpdateLease(l); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := h.st.GetLease(id)
	if err != nil {
		c.JSON(http.StatusOK, l)
		return
	}
	c.JSON(http.StatusOK, out)
}

// SetLeaseStatus 退租/恢复在租（body: {status: active|ended}）。
func (h *HouseHandler) SetLeaseStatus(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（status 必填）"})
		return
	}
	if err := h.st.SetLeaseStatus(id, req.Status); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// RenewLease 续租：当前租约退租留档 + 复制生成新租约（起始=旧截止次日，
// 默认一年，押金/租客/出租方/读数沿用），返回新租约。
func (h *HouseHandler) RenewLease(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	l, err := h.st.RenewLease(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, l)
}

// RemoveLease 删除租约。
func (h *HouseHandler) RemoveLease(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.st.DeleteLease(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Contract 按租约生成并下载租赁合同 docx（文档附件同款 attachment 头，
// mime.FormatMediaType 处理中文文件名 RFC 2231 编码）。
func (h *HouseHandler) Contract(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	l, err := h.st.GetLease(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	house, err := h.st.GetHouse(l.HouseID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	items, err := h.st.ListHouseItems(l.HouseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询家具电器失败"})
		return
	}
	data, err := houses.BuildContract(l, house, items, h.customTemplate())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	filename := fmt.Sprintf("房屋租赁合同-%s-%s.docx", house.Name, l.TenantName)
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", data)
}

// TestLeasePush 立即按租约到期文案发一次测试推送：站内消息必发，三渠道按
// 「提醒事项」推送开关分发（off = 开关未开，ok = 已推送，其余为失败原因）。
// 与会员到期测试推送同口径；消息类型 house（不入提醒红字联查）。
func (h *HouseHandler) TestLeasePush(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	l, err := h.st.GetLease(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	subject := "租约到期提醒：" + l.HouseName
	body := fmt.Sprintf("「%s」租客 %s 的租约剩余 %d 天（%s 到期）。〔测试推送〕", l.HouseName, l.TenantName, l.RemainingDays, l.EndDate)
	msg := &store.Message{
		Type: "house", Title: subject, Content: body,
		RefType: "house_lease", RefID: l.ID, UserID: 0, CreatedAt: time.Now().Unix(),
	}
	if err := h.st.CreateMessage(msg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "站内消息写入失败"})
		return
	}
	res := gin.H{}
	if wxpusher.On(h.st) && wxpusher.ReminderWXOn(h.st) {
		res["wxpusher"] = "ok"
		if err := wxpusher.SendFromStore(h.st, subject, body); err != nil {
			res["wxpusher"] = err.Error()
		}
	} else {
		res["wxpusher"] = "off"
	}
	if webhook.On(h.st) && webhook.ReminderOn(h.st) {
		res["webhook"] = "ok"
		if err := webhook.SendFromStore(h.st, subject, body); err != nil {
			res["webhook"] = err.Error()
		}
	} else {
		res["webhook"] = "off"
	}
	if webpush.On(h.st) && webpush.ReminderOn(h.st) {
		res["webpush"] = "ok"
		if err := webpush.SendFromStore(h.st, subject, body); err != nil {
			res["webpush"] = err.Error()
		}
	} else {
		res["webpush"] = "off"
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "results": res})
}

type houseExpenseReq struct {
	HouseID   int64   `json:"house_id" binding:"required"`
	Name      string  `json:"name" binding:"required"`
	Amount    float64 `json:"amount"`
	SpendDate string  `json:"spend_date" binding:"required"`
	Notes     string  `json:"notes"`
}

// CreateExpense 新建消耗费用（桌椅/电器置换、取暖费等）。
func (h *HouseHandler) CreateExpense(c *gin.Context) {
	var req houseExpenseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（房屋、费用名称与日期必填）"})
		return
	}
	e := &store.HouseExpense{HouseID: req.HouseID, Name: req.Name, Amount: req.Amount, SpendDate: req.SpendDate, Notes: req.Notes}
	if err := h.st.CreateHouseExpense(e); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, e)
}

// UpdateExpense 编辑消耗费用。
func (h *HouseHandler) UpdateExpense(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req houseExpenseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（费用名称与日期必填）"})
		return
	}
	e := &store.HouseExpense{ID: id, Name: req.Name, Amount: req.Amount, SpendDate: req.SpendDate, Notes: req.Notes}
	if err := h.st.UpdateHouseExpense(e); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, e)
}

// RemoveExpense 删除消耗费用。
func (h *HouseHandler) RemoveExpense(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.st.DeleteHouseExpense(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Income 收支统计（?year=YYYY，缺省当年）：年度与四个季度各返回
// 总收入/消耗费用/实际收入（总收入=租期覆盖天数折算应收租金，含已退租历史
// 租约；消耗费用=期间费用合计；实际收入=总收入−消耗费用）。
func (h *HouseHandler) Income(c *gin.Context) {
	year, _ := strconv.Atoi(c.Query("year"))
	if year < 2000 || year > 2999 {
		year = time.Now().Year()
	}
	inc, err := h.st.HouseIncomeRange(fmt.Sprintf("%d-01-01", year), fmt.Sprintf("%d-12-31", year))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "统计年度收支失败"})
		return
	}
	quarters := make([]gin.H, 0, 4)
	qDays := [][2]string{{"01-01", "03-31"}, {"04-01", "06-30"}, {"07-01", "09-30"}, {"10-01", "12-31"}}
	for i, d := range qDays {
		qi, err := h.st.HouseIncomeRange(fmt.Sprintf("%d-%s", year, d[0]), fmt.Sprintf("%d-%s", year, d[1]))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "统计季度收支失败"})
			return
		}
		quarters = append(quarters, gin.H{"q": i + 1, "gross": qi.Gross, "expense": qi.Expense, "net": qi.Net})
	}
	c.JSON(http.StatusOK, gin.H{"year": year, "year_income": gin.H{"gross": inc.Gross, "expense": inc.Expense, "net": inc.Net}, "quarters": quarters})
}

// ---- 合同模板管理（2026-11-04）：统一的租房合同模板可上传替换，内置标准
// 模板兜底；模板存 settings（docx 原件 base64），生成时只换 word/document.xml
// 保留其余部件（样式/页眉/图片） ----

// 合同模板 settings 键与大小上限（5MB，普通合同模板几十 KB 足够）。
const (
	houseTplKey     = "house_contract_template"
	houseTplNameKey = "house_contract_template_name"
	houseTplAtKey   = "house_contract_template_at"
	houseTplMaxSize = 5 << 20
)

// customTemplate 当前自定义模板（docx 原件字节；未配置/解码失败返回 nil 走内置）。
func (h *HouseHandler) customTemplate() []byte {
	v, ok, err := h.st.GetSetting(houseTplKey)
	if err != nil || !ok || v == "" {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil
	}
	return data
}

// TemplateInfo 当前模板状态：{custom, name, uploaded_at}。
func (h *HouseHandler) TemplateInfo(c *gin.Context) {
	v, ok, _ := h.st.GetSetting(houseTplKey)
	res := gin.H{"custom": ok && v != ""}
	if res["custom"] == true {
		name, _, _ := h.st.GetSetting(houseTplNameKey)
		at, _, _ := h.st.GetSetting(houseTplAtKey)
		res["name"] = name
		res["uploaded_at"] = at
	}
	c.JSON(http.StatusOK, res)
}

// TemplateUpload 上传自定义合同模板：JSON {filename, data=docx base64}。
// 上传时校验是合法 docx（zip 且含 word/document.xml）。body 预限 8MB
// （5MB 模板 base64 后约 6.7MB + JSON 包裹，超限直接拒收不做全量解码）。
func (h *HouseHandler) TemplateUpload(c *gin.Context) {
	c.Request.Body = io.NopCloser(io.LimitReader(c.Request.Body, 8<<20))
	var req struct {
		Filename string `json:"filename"`
		Data     string `json:"data" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（模板文件必填）或文件过大（上限 5MB）"})
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil || len(data) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模板数据解码失败"})
		return
	}
	if len(data) > houseTplMaxSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模板文件过大（上限 5MB）"})
		return
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不是合法的 docx 文件，请上传 Word 文档（.docx）"})
		return
	}
	found := false
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			found = true
			break
		}
	}
	if !found {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模板缺少 word/document.xml，请上传 Word 文档（.docx）"})
		return
	}
	if err := h.st.SetSetting(houseTplKey, base64.StdEncoding.EncodeToString(data)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存模板失败"})
		return
	}
	_ = h.st.SetSetting(houseTplNameKey, req.Filename)
	_ = h.st.SetSetting(houseTplAtKey, time.Now().Format("2006-01-02 15:04"))
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// TemplateRemove 恢复内置标准模板（删除自定义模板配置行）。
func (h *HouseHandler) TemplateRemove(c *gin.Context) {
	_ = h.st.DeleteSetting(houseTplKey)
	_ = h.st.DeleteSetting(houseTplNameKey)
	_ = h.st.DeleteSetting(houseTplAtKey)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
