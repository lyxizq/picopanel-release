package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// MedicationHandler 用药提醒（2026-11-02，参考 Apple Health 药物，分家庭成员）：
// 人员/药物 CRUD + 剂量打卡 + 今日/月历/依从率视图 + 库存。
// 家庭成员口径（2026-11-03）：管理员看全部人员并管理（代管）；受限用户只看到/
// 操作自己绑定的人员（首次进页自动建人，MedPersonByUser），人员管理端点 403，
// 药物/打卡/视图强制 person 维度=绑定人，跨空间一律「不存在」404 语义。
type MedicationHandler struct {
	st *store.Store
}

// NewMedicationHandler 创建 handler。
func NewMedicationHandler(st *store.Store) *MedicationHandler { return &MedicationHandler{st: st} }

// medBoundPersonID 受限用户的绑定人员 ID（无绑定自动创建）；管理员返回 0=不限
// 制（看全部）。store 出错时已写 500 响应，返回 (0,false)，调用方直接 return。
func (h *MedicationHandler) medBoundPersonID(c *gin.Context) (int64, bool) {
	uid := scopeUID(c)
	if uid == 0 {
		return 0, true
	}
	p, err := h.st.MedPersonByUser(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询绑定的家庭成员失败"})
		return 0, false
	}
	return p.ID, true
}

// medOwnsBound 受限用户校验药物归属绑定人（跨空间=不存在 404 语义，不泄露存在
// 性）；管理员恒通过。personID 为当前会话允许的 person 维度（管理员传 0）。
func (h *MedicationHandler) medOwnsBound(c *gin.Context, personID, medID int64) bool {
	if personID == 0 {
		return true
	}
	m, err := h.st.GetMedication(medID)
	if err != nil || m.PersonID != personID {
		c.JSON(http.StatusNotFound, gin.H{"error": "药物不存在"})
		return false
	}
	return true
}

// ---- 家庭成员 ----

// PersonList 家庭成员（带名下药物数）：管理员=全部；受限用户=只返回自己绑定
// 的那个人。
func (h *MedicationHandler) PersonList(c *gin.Context) {
	uid := scopeUID(c)
	if uid > 0 {
		p, err := h.st.MedPersonByUser(uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询绑定的家庭成员失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": []store.MedPerson{*p}})
		return
	}
	list, err := h.st.ListMedPersons()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询家庭成员失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": list})
}

type medPersonReq struct {
	Name  string `json:"name" binding:"required"`
	Color string `json:"color"`
}

// PersonCreate 新建家庭成员（家庭成员由管理员统一管理，受限用户 403）。
func (h *MedicationHandler) PersonCreate(c *gin.Context) {
	if scopeUID(c) != 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "家庭成员由管理员统一管理"})
		return
	}
	var req medPersonReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（姓名必填）"})
		return
	}
	p := &store.MedPerson{Name: req.Name, Color: req.Color}
	if err := h.st.CreateMedPerson(p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

// PersonUpdate 编辑家庭成员（受限用户 403）。
func (h *MedicationHandler) PersonUpdate(c *gin.Context) {
	if scopeUID(c) != 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "家庭成员由管理员统一管理"})
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req medPersonReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（姓名必填）"})
		return
	}
	p := &store.MedPerson{ID: id, Name: req.Name, Color: req.Color}
	if err := h.st.UpdateMedPerson(p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := h.st.GetMedPerson(id)
	if err != nil {
		c.JSON(http.StatusOK, p)
		return
	}
	c.JSON(http.StatusOK, out)
}

// PersonDelete 删除家庭成员（级联删其药物与剂量台账；受限用户 403）。
func (h *MedicationHandler) PersonDelete(c *gin.Context) {
	if scopeUID(c) != 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "家庭成员由管理员统一管理"})
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.st.DeleteMedPerson(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// PersonReorder 拖拽排序保存（/med-persons-reorder 静态前缀，memberships 同款
// 避让；受限用户 403）。
func (h *MedicationHandler) PersonReorder(c *gin.Context) {
	if scopeUID(c) != 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "家庭成员由管理员统一管理"})
		return
	}
	var req struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（ids 必填）"})
		return
	}
	if err := h.st.ReorderMedPersons(req.IDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存排序失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---- 药物 ----

// List 药物列表（person_id 过滤、archived=1 带归档；受限用户强制只看绑定人
// 名下，请求里的 person_id 忽略）。
func (h *MedicationHandler) List(c *gin.Context) {
	personID, ok := h.medBoundPersonID(c)
	if !ok {
		return
	}
	if personID == 0 {
		personID, _ = strconv.ParseInt(c.Query("person_id"), 10, 64)
	}
	list, err := h.st.ListMedications(personID, c.Query("archived") == "1")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询药物失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": list})
}

type medicationReq struct {
	PersonID     int64    `json:"person_id" binding:"required"`
	Name         string   `json:"name" binding:"required"`
	Note         string   `json:"note"`
	Form         string   `json:"form"`
	DoseAmount   float64  `json:"dose_amount"`
	DoseUnit     string   `json:"dose_unit"`
	Kind         string   `json:"kind"`
	Times        []string `json:"times"`
	Weekdays     []int    `json:"weekdays"`
	IntervalDays int      `json:"interval_days"`
	StartDate    string   `json:"start_date"`
	DurationDays int      `json:"duration_days"`
	StockQty     *float64 `json:"stock_qty"`
	StockAlert   float64  `json:"stock_alert"`
	Archived     bool     `json:"archived"`
}

func (h *MedicationHandler) reqToMed(req *medicationReq) *store.Medication {
	m := &store.Medication{
		PersonID: req.PersonID, Name: req.Name, Note: req.Note, Form: req.Form,
		DoseAmount: req.DoseAmount, DoseUnit: req.DoseUnit, Kind: req.Kind,
		Times: req.Times, Weekdays: req.Weekdays, IntervalDays: req.IntervalDays,
		StartDate: req.StartDate, DurationDays: req.DurationDays,
		StockQty: req.StockQty, StockAlert: req.StockAlert,
	}
	if req.Archived {
		now := time.Now().Unix()
		m.ArchivedAt = &now
	}
	return m
}

// Create 新建药物（建完立即按新时间表排当天剂量；受限用户的 person 维度强制
// =绑定人，请求里的 person_id 忽略）。
func (h *MedicationHandler) Create(c *gin.Context) {
	personID, ok := h.medBoundPersonID(c)
	if !ok {
		return
	}
	var req medicationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（家庭成员与药名必填）"})
		return
	}
	if personID > 0 {
		req.PersonID = personID
	}
	m := h.reqToMed(&req)
	if err := h.st.CreateMedication(m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.st.EnsureMedDay(time.Now().Format("2006-01-02")); err != nil {
		c.JSON(http.StatusOK, m)
		return
	}
	out, _ := h.st.GetMedication(m.ID)
	if out == nil {
		out = m
	}
	c.JSON(http.StatusOK, out)
}

// Update 编辑药物（时间表变动自动清今天起 pending 由日结重排；受限用户只许
// 编辑绑定人名下的药，跨空间=不存在 404，person 维度强制=绑定人）。
func (h *MedicationHandler) Update(c *gin.Context) {
	personID, ok := h.medBoundPersonID(c)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if !h.medOwnsBound(c, personID, id) {
		return
	}
	var req medicationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（家庭成员与药名必填）"})
		return
	}
	if personID > 0 {
		req.PersonID = personID
	}
	m := h.reqToMed(&req)
	m.ID = id
	if err := h.st.UpdateMedication(m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.st.EnsureMedDay(time.Now().Format("2006-01-02")); err != nil {
		c.JSON(http.StatusOK, m)
		return
	}
	out, err := h.st.GetMedication(id)
	if err != nil {
		c.JSON(http.StatusOK, m)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Delete 删除药物（级联删剂量台账；受限用户只许删绑定人名下的药，跨空间 404）。
func (h *MedicationHandler) Delete(c *gin.Context) {
	personID, ok := h.medBoundPersonID(c)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if !h.medOwnsBound(c, personID, id) {
		return
	}
	if err := h.st.DeleteMedication(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Reorder 拖拽排序保存（/medications-reorder 静态前缀，memberships 同款避让；
// 受限用户只许排绑定人名下的药，混入跨空间 id 整体 404）。
func (h *MedicationHandler) Reorder(c *gin.Context) {
	personID, ok := h.medBoundPersonID(c)
	if !ok {
		return
	}
	var req struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（ids 必填）"})
		return
	}
	for _, id := range req.IDs {
		if !h.medOwnsBound(c, personID, id) {
			return
		}
	}
	if err := h.st.ReorderMedications(req.IDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存排序失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Stock 手动改库存（补货后改数字即重置补货提醒标记；受限用户只许改绑定人
// 名下的药，跨空间 404）。
func (h *MedicationHandler) Stock(c *gin.Context) {
	personID, ok := h.medBoundPersonID(c)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if !h.medOwnsBound(c, personID, id) {
		return
	}
	var req struct {
		Qty float64 `json:"qty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（qty 必填）"})
		return
	}
	if err := h.st.UpdateMedStock(id, req.Qty); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := h.st.GetMedication(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 0})
		return
	}
	c.JSON(http.StatusOK, out)
}

// ---- 打卡与视图 ----

// Day 某天剂量时间轴（date=YYYY-MM-DD，person_id 可选过滤；受限用户强制只看
// 绑定人的剂量）。
func (h *MedicationHandler) Day(c *gin.Context) {
	day := c.Query("date")
	if day == "" {
		day = time.Now().Format("2006-01-02")
	}
	if _, err := time.ParseInLocation("2006-01-02", day, time.Local); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "日期格式应为 YYYY-MM-DD"})
		return
	}
	personID, ok := h.medBoundPersonID(c)
	if !ok {
		return
	}
	if personID == 0 {
		personID, _ = strconv.ParseInt(c.Query("person_id"), 10, 64)
	}
	list, err := h.st.MedDosesView(day, personID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询剂量失败"})
		return
	}
	if list == nil {
		list = []store.MedDose{}
	}
	c.JSON(http.StatusOK, gin.H{"items": list})
}

type medLogReq struct {
	MedID  int64  `json:"med_id" binding:"required"`
	Day    string `json:"day"`
	Time   string `json:"time" binding:"required"`
	Status string `json:"status" binding:"required"`
}

// Log 打卡：taken/skipped（可补卡改 missed）。day 缺省=今天。受限用户只许打卡
// 绑定人名下的药（跨空间=不存在 404）。
// LogComplete POST /meds/log-complete/:id 提醒窗口直达打卡（2026-11-03）：站内
// 消息（ref_id=med_logs 行 ID）与 Web Push 通知「完成」按钮按行 ID 直置 taken
// ——复用 SetMedLogStatus 状态机（含库存联动；missed 补卡=变 taken 补扣库存）。
// 归属校验同 /meds/log（成员只能完成绑定人名下的剂量）。
func (h *MedicationHandler) LogComplete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的剂量记录 ID"})
		return
	}
	d, err := h.st.MedLogByID(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询剂量记录失败"})
		return
	}
	if d == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "剂量记录不存在"})
		return
	}
	personID, ok := h.medBoundPersonID(c)
	if !ok {
		return
	}
	if !h.medOwnsBound(c, personID, d.MedID) {
		return
	}
	if err := h.st.SetMedLogStatus(d.MedID, d.Day, d.Time, "taken", time.Now().Unix()); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

func (h *MedicationHandler) Log(c *gin.Context) {
	personID, ok := h.medBoundPersonID(c)
	if !ok {
		return
	}
	var req medLogReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（med_id/time/status 必填）"})
		return
	}
	if !h.medOwnsBound(c, personID, req.MedID) {
		return
	}
	day := req.Day
	if day == "" {
		day = time.Now().Format("2006-01-02")
	}
	if err := h.st.SetMedLogStatus(req.MedID, day, req.Time, req.Status, time.Now().Unix()); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Prn 按需药记一笔（以当前时刻落 taken 台账并扣库存；受限用户只许绑定人名下
// 的按需药，跨空间=不存在 404）。
func (h *MedicationHandler) Prn(c *gin.Context) {
	personID, ok := h.medBoundPersonID(c)
	if !ok {
		return
	}
	var req struct {
		MedID int64 `json:"med_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（med_id 必填）"})
		return
	}
	if !h.medOwnsBound(c, personID, req.MedID) {
		return
	}
	if err := h.st.RecordMedPrn(req.MedID, time.Now().Unix()); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Month 月历逐天汇总（month=YYYY-MM；受限用户只统计绑定人的剂量）。
func (h *MedicationHandler) Month(c *gin.Context) {
	month := c.Query("month")
	if _, err := time.ParseInLocation("2006-01", month, time.Local); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "月份格式应为 YYYY-MM"})
		return
	}
	personID, ok := h.medBoundPersonID(c)
	if !ok {
		return
	}
	list, err := h.st.MedMonthSummary(month, personID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询月历失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": list})
}

// Adherence 依从率区间聚合（days=7/30/90；受限用户只统计绑定人的剂量）。
func (h *MedicationHandler) Adherence(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	if days != 7 && days != 30 && days != 90 {
		days = 7
	}
	personID, ok := h.medBoundPersonID(c)
	if !ok {
		return
	}
	today := time.Now()
	from := today.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	total, taken, skipped, missed, err := h.st.MedAdherence(from, today.Format("2006-01-02"), personID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询依从率失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"days": days, "total": total, "taken": taken, "skipped": skipped, "missed": missed})
}
