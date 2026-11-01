package api

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/mail"
	"lz-monitor/internal/server/store"
)

// MailHandler 邮件收发接口（316-318，登录组）：账户 CRUD/测试/排序、文件夹、
// 信头列表（缓存+IMAP）、正文阅读、已读/删除、附件下载、SMTP 发信、未读角标。
type MailHandler struct {
	st *store.Store
	mg *mail.Manager
}

// NewMailHandler 构造。
func NewMailHandler(st *store.Store, mg *mail.Manager) *MailHandler {
	return &MailHandler{st: st, mg: mg}
}

// mailAccountReq 账户增改请求（密码空=改密沿用旧值）。
type mailAccountReq struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Provider string `json:"provider"`
	IMAPHost string `json:"imap_host"`
	IMAPPort *int   `json:"imap_port"`
	SMTPHost string `json:"smtp_host"`
	SMTPPort *int   `json:"smtp_port"`
	SMTPSSL  *bool  `json:"smtp_ssl"`
	Password string `json:"password"`
	PollSec  *int   `json:"poll_sec"` // 未读轮询间隔秒（默认 600，60~86400）
}

func (h *MailHandler) view(a *store.MailAccount) gin.H {
	return gin.H{"id": a.ID, "name": a.Name, "email": a.Email, "provider": a.Provider,
		"imap_host": a.IMAPHost, "imap_port": a.IMAPPort, "smtp_host": a.SMTPHost,
		"smtp_port": a.SMTPPort, "smtp_ssl": a.SMTPSSL, "has_auth": a.HasAuth,
		"poll_sec": a.PollSec,
		"sort":     a.Sort, "last_ok_at": a.LastOKAt, "last_error": a.LastError,
		"unread": h.mg.Unread(a.ID)}
}

func (h *MailHandler) presets(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"presets": store.MailProviderPresets()})
}

func (h *MailHandler) accounts(c *gin.Context) {
	list, err := h.st.ListMailAccountMetas()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询邮件账户失败"})
		return
	}
	out := make([]gin.H, 0, len(list))
	for i := range list {
		out = append(out, h.view(&list[i]))
	}
	c.JSON(http.StatusOK, gin.H{"accounts": out, "max": store.MaxMailAccounts})
}

// buildAccount 请求 → 账户（预设补全服务器字段；custom 全手填）。
func buildAccount(req *mailAccountReq) (*store.MailAccount, error) {
	req.Email = strings.TrimSpace(req.Email)
	req.Name = strings.TrimSpace(req.Name)
	req.IMAPHost = strings.TrimSpace(req.IMAPHost)
	req.SMTPHost = strings.TrimSpace(req.SMTPHost)
	if req.Email == "" || !strings.Contains(req.Email, "@") ||
		strings.IndexFunc(req.Email, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		// 只放可打印 ASCII（挡控制字符/空格/非 ASCII）：email 会拼进发信
		// From: 头并作 IMAP/SMTP 登录名，混入 \r\n 可注入任意信头
		return nil, fmt.Errorf("邮箱地址不合法")
	}
	if req.Name == "" {
		req.Name = strings.SplitN(req.Email, "@", 2)[0]
	}
	poll := store.DefaultMailPollSec
	if req.PollSec != nil {
		poll = *req.PollSec
	}
	a := &store.MailAccount{Name: req.Name, Email: req.Email, Provider: req.Provider,
		IMAPHost: req.IMAPHost, IMAPPort: 993, SMTPHost: req.SMTPHost, SMTPPort: 465, SMTPSSL: true,
		PollSec: poll}
	if p := store.MailProviderByKey(req.Provider); p != nil && p.Key != "custom" {
		a.IMAPHost, a.IMAPPort = p.IMAPHost, p.IMAPPort
		a.SMTPHost, a.SMTPPort, a.SMTPSSL = p.SMTPHost, p.SMTPPort, p.SMTPSSL
	}
	if req.IMAPPort != nil {
		a.IMAPPort = *req.IMAPPort
	}
	if req.SMTPPort != nil {
		a.SMTPPort = *req.SMTPPort
	}
	if req.SMTPSSL != nil {
		a.SMTPSSL = *req.SMTPSSL
	}
	if a.IMAPHost == "" || a.IMAPPort < 1 || a.IMAPPort > 65535 || a.SMTPHost == "" || a.SMTPPort < 1 || a.SMTPPort > 65535 {
		return nil, fmt.Errorf("IMAP/SMTP 服务器地址或端口不合法")
	}
	return a, nil
}

func (h *MailHandler) create(c *gin.Context) {
	var req mailAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if n, _ := h.st.CountMailAccounts(); n >= store.MaxMailAccounts {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("最多 %d 个邮箱账户", store.MaxMailAccounts)})
		return
	}
	if strings.TrimSpace(req.Password) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写密码/授权码"})
		return
	}
	a, err := buildAccount(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a.Password = strings.TrimSpace(req.Password)
	// 先测后存：连不上不给存（省得存下一堆死配置）
	if err := mail.TestAccount(*a, a.Password); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "连接测试失败: " + err.Error()})
		return
	}
	id, err := h.st.CreateMailAccount(a)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	h.mg.RefreshAll()
	c.JSON(http.StatusOK, gin.H{"code": 0, "id": id})
}

func (h *MailHandler) account(c *gin.Context) *store.MailAccount {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return nil
	}
	a, err := h.st.GetMailAccount(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "邮件账户不存在"})
		return nil
	}
	return a
}

func (h *MailHandler) update(c *gin.Context) {
	var req mailAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	a, err := buildAccount(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a.ID, err = strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || a.ID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	old, err := h.st.GetMailAccount(a.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "邮件账户不存在"})
		return
	}
	pwd := strings.TrimSpace(req.Password)
	if pwd != "" {
		a.Password = pwd // 新密码先测再存
		if err := mail.TestAccount(*a, pwd); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "连接测试失败: " + err.Error()})
			return
		}
	} else {
		a.Password = old.Password
	}
	if err := h.st.UpdateMailAccount(a); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	h.mg.RefreshAll()
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

func (h *MailHandler) remove(c *gin.Context) {
	a := h.account(c)
	if a == nil {
		return
	}
	if err := h.st.DeleteMailAccount(a.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	h.mg.Drop(a.ID)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

func (h *MailHandler) order(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := h.st.UpdateMailAccountsOrder(req.IDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存排序失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// testAccountBody 测试连接请求（编辑态可另给密码试连）。
type testAccountBody struct {
	Password string `json:"password"`
}

func (h *MailHandler) test(c *gin.Context) {
	a := h.account(c)
	if a == nil {
		return
	}
	var body testAccountBody
	_ = c.ShouldBindJSON(&body)
	if err := mail.TestAccount(*a, strings.TrimSpace(body.Password)); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	_ = h.st.RecordMailAccountStatus(a.ID, true, "")
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// folders 账户的文件夹清单（直连 IMAP LIST）。
func (h *MailHandler) folders(c *gin.Context) {
	a := h.account(c)
	if a == nil {
		return
	}
	cl, err := mail.DialIMAP(*a)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer cl.Close()
	folders, err := cl.Folders()
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"folders": folders})
}

// messages 信头列表（2026-10-03 用户指定改本地库直出）：默认从 mail_headers
// 缓存查询（打开/切换邮箱秒开，数据由轮询与刷新维护）；refresh=1（手动刷新）
// 或带 search 时才连 IMAP——刷新整夹以服务器为准，搜索需服务器 SEARCH 全库匹配。
// before_uid（加载更多）默认走 IMAP 深翻页并增量并入缓存（缓存窗口随浏览加深）。
func (h *MailHandler) messages(c *gin.Context) {
	a := h.account(c)
	if a == nil {
		return
	}
	folder := c.Query("folder")
	if folder == "" {
		folder = "INBOX"
	}
	term := strings.TrimSpace(c.Query("search"))
	limit := 50
	if v := c.Query("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var beforeUID int64
	if v := c.Query("before_uid"); v != "" {
		fmt.Sscanf(v, "%d", &beforeUID)
	}
	refresh := c.Query("refresh") == "1"

	// 本地库直出：首屏默认路径（无搜索、无刷新、非深翻页）
	if term == "" && !refresh && beforeUID == 0 {
		headers, err := h.st.ListMailHeadersPage(a.ID, folder, limit+1, 0)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取本地邮件缓存失败"})
			return
		}
		// 整页即视为可能还有更早的：缓存窗口只含已同步的最新一段（轮询/刷新各
		// 留 50 封），查本地判断不出服务器还有没有——取满一页就给 true 让
		// 「查看更多」出现，深翻页走 IMAP，没有更早的自然收口回 false
		// （2026-10-04 用户反馈按钮不再显示）
		hasMore := len(headers) >= limit
		if len(headers) > limit {
			headers = headers[:limit]
		}
		c.JSON(http.StatusOK, gin.H{"headers": headers, "has_more": hasMore})
		return
	}

	cl, err := mail.DialIMAP(*a)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer cl.Close()
	headers, err := cl.List(folder, term, limit, beforeUID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if term == "" { // 刷新/深翻页同步缓存：首屏整夹替换（服务器为准），翻页增量合并
		if beforeUID == 0 {
			_ = h.st.ReplaceMailHeaders(a.ID, folder, toCacheHeaders(headers))
		} else {
			_ = h.st.UpsertMailHeaders(a.ID, folder, toCacheHeaders(headers))
		}
	}
	c.JSON(http.StatusOK, gin.H{"headers": headers, "has_more": len(headers) >= limit})
}

func toCacheHeaders(list []mail.Header) []store.MailHeader {
	out := make([]store.MailHeader, 0, len(list))
	for _, h := range list {
		out = append(out, store.MailHeader{UID: h.UID, FromName: h.FromName, FromAddr: h.FromAddr,
			Subject: h.Subject, DateUnix: h.DateUnix, Seen: h.Seen, HasAtt: h.HasAtt, Size: h.Size})
	}
	return out
}

// cachedMessages 缓存兜底（IMAP 不可达时列表仍可看）。
func (h *MailHandler) cached(c *gin.Context) {
	a := h.account(c)
	if a == nil {
		return
	}
	folder := c.DefaultQuery("folder", "INBOX")
	headers, err := h.st.ListMailHeaders(a.ID, folder, 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取缓存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"headers": headers})
}

// read 阅读一封（整封拉回解析：正文 text/html + 附件清单）。
func (h *MailHandler) read(c *gin.Context) {
	a := h.account(c)
	if a == nil {
		return
	}
	folder, uid, ok := mailTarget(c)
	if !ok {
		return
	}
	cl, err := mail.DialIMAP(*a)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer cl.Close()
	msg, err := cl.ReadMessage(folder, uid)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, msg)
}

// seen 标记已读/未读（IMAP + 缓存同步）。
func (h *MailHandler) seen(c *gin.Context) {
	a := h.account(c)
	if a == nil {
		return
	}
	var req struct {
		Folder string `json:"folder"`
		UID    int64  `json:"uid"`
		Seen   bool   `json:"seen"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.UID <= 0 || req.Folder == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	cl, err := mail.DialIMAP(*a)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer cl.Close()
	if err := cl.MarkSeen(req.Folder, req.UID, req.Seen); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	// 未读缓存即时修正（372）：仅 INBOX、本地缓存有旧态且翻转才 ∓1，否则等轮询自愈
	if req.Folder == "INBOX" {
		if oldSeen, ok, err := h.st.MailHeaderSeen(a.ID, req.Folder, req.UID); err == nil && ok && oldSeen != req.Seen {
			if req.Seen {
				h.mg.AdjustUnread(a.ID, -1)
			} else {
				h.mg.AdjustUnread(a.ID, 1)
			}
		}
	}
	_ = h.st.SetMailHeaderSeen(a.ID, req.Folder, req.UID, req.Seen)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// removeMsgs 删除邮件（移入垃圾桶或原地清掉，支持批量）。
func (h *MailHandler) removeMsgs(c *gin.Context) {
	a := h.account(c)
	if a == nil {
		return
	}
	var req struct {
		Folder string  `json:"folder"`
		UIDs   []int64 `json:"uids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Folder == "" || len(req.UIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	cl, err := mail.DialIMAP(*a)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer cl.Close()
	if err := cl.DeleteMessages(req.Folder, req.UIDs); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	// 未读缓存即时修正（372）：INBOX 删除行里未读的封数递减（旧态读不到的行不计）
	if req.Folder == "INBOX" {
		delta := 0
		for _, uid := range req.UIDs {
			if oldSeen, ok, err := h.st.MailHeaderSeen(a.ID, req.Folder, uid); err == nil && ok && !oldSeen {
				delta--
			}
		}
		h.mg.AdjustUnread(a.ID, delta)
	}
	_ = h.st.DeleteMailHeaders(a.ID, req.Folder, req.UIDs) // 列表本地库直出，缓存同步清行
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// attachment 附件下载（按 IMAP part 路径抽取，流式回传原文件名）。
func (h *MailHandler) attachment(c *gin.Context) {
	a := h.account(c)
	if a == nil {
		return
	}
	folder, uid, ok := mailTarget(c)
	if !ok {
		return
	}
	part := strings.TrimSpace(c.Query("part"))
	if part == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少附件 part"})
		return
	}
	// 附件要现连 IMAP 拉整封原文再回传，耗时远超普通 JSON 接口——解除全局 30s
	// 写超时：超时会被掐断连接、浏览器 fetch 报 Failed to fetch（口径同 SFTP 下载）
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})
	cl, err := mail.DialIMAP(*a)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer cl.Close()
	name, ct, data, err := cl.FetchAttachment(folder, uid, part)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	// 附件按原样下发（邮件附件本就不可信，前端以 download 属性触发保存）
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", rfc5987(name)))
	ctType := ct
	if ctType == "" {
		ctType = "application/octet-stream"
	}
	c.Data(http.StatusOK, ctType, data)
}

// send 写信/回复/转发（表单：to/cc/subject/text/html/in_reply_to + 附件文件）。
func (h *MailHandler) send(c *gin.Context) {
	a := h.account(c)
	if a == nil {
		return
	}
	// 发信带附件是大 multipart body：慢客户端上传会超全局 30s 读超时，后续
	// SMTP 发送大信也耗时——读写超时一并解除（口径同文件上传接口）
	rc := http.NewResponseController(c.Writer)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "表单解析失败"})
		return
	}
	val := func(k string) string {
		if len(form.Value[k]) == 0 {
			return ""
		}
		return strings.TrimSpace(form.Value[k][0])
	}
	m := &mail.OutMail{
		To:        val("to"),
		CC:        val("cc"),
		Subject:   val("subject"),
		InReplyTo: val("in_reply_to"),
	}
	if len(form.Value["html"]) > 0 {
		m.HTML = form.Value["html"][0]
	}
	if len(form.Value["text"]) > 0 {
		m.Text = form.Value["text"][0]
	}
	pwd := val("password")
	if !a.HasAuth && pwd == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "账户缺少授权码"})
		return
	}
	files := form.File["attachments"]
	var total int64
	for _, fh := range files {
		total += fh.Size
	}
	if total > mail.MaxOutTotal {
		c.JSON(http.StatusBadRequest, gin.H{"error": "附件总量超 25MB"})
		return
	}
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "附件读取失败"})
			return
		}
		content, err := io.ReadAll(io.LimitReader(f, mail.MaxOutTotal))
		f.Close()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "附件读取失败"})
			return
		}
		m.Attachments = append(m.Attachments, mail.OutAttachment{
			Name: fh.Filename, CT: fh.Header.Get("Content-Type"), Content: content})
	}
	if err := mail.SendMail(*a, val("password"), m); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	_ = h.st.RecordMailAccountStatus(a.ID, true, "")
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// badge 侧栏角标：各账户 INBOX 未读数（Manager 轮询缓存，不现查 IMAP）。
func (h *MailHandler) badge(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"unread": h.mg.UnreadAll()})
}

// ---- 通讯录（506）/ 个人签名（507） ----

// validContactEmail 邮箱校验：含 @ 且只放可打印 ASCII（口径同账户 email——
// 虽不直接拼发信头，但会存库回显，收紧省得脏数据进通讯录）。
func validContactEmail(email string) bool {
	if email == "" || !strings.Contains(email, "@") ||
		strings.IndexFunc(email, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return false
	}
	return true
}

const maxMailContacts = 500

func (h *MailHandler) contacts(c *gin.Context) {
	list, err := h.st.ListMailContacts()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询通讯录失败"})
		return
	}
	if list == nil {
		list = []store.MailContact{}
	}
	c.JSON(http.StatusOK, gin.H{"contacts": list})
}

// contactReq 通讯录增改请求（name 可空，只作展示与搜索）。
type contactReq struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (h *MailHandler) addContact(c *gin.Context) {
	var req contactReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if !validContactEmail(strings.TrimSpace(req.Email)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "邮箱地址不合法"})
		return
	}
	list, _ := h.st.ListMailContacts()
	if len(list) >= maxMailContacts {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("通讯录最多 %d 条", maxMailContacts)})
		return
	}
	ct, err := h.st.CreateMailContact(req.Name, req.Email)
	if err != nil {
		if err == store.ErrMailContactExists {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "contact": ct})
}

func (h *MailHandler) editContact(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	var req contactReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if !validContactEmail(strings.TrimSpace(req.Email)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "邮箱地址不合法"})
		return
	}
	ct, err := h.st.UpdateMailContact(id, req.Name, req.Email)
	if err != nil {
		if err == store.ErrMailContactExists || err == store.ErrMailContactNotFound {
			status := http.StatusBadRequest
			if err == store.ErrMailContactNotFound {
				status = http.StatusNotFound
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "contact": ct})
}

func (h *MailHandler) delContact(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := h.st.DeleteMailContact(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// getSignature 个人签名（settings kv，未配置为空串）。
func (h *MailHandler) getSignature(c *gin.Context) {
	sig, err := h.st.MailSignature()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取签名失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"signature": sig})
}

func (h *MailHandler) setSignature(c *gin.Context) {
	var req struct {
		Signature string `json:"signature"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	sig := strings.TrimRight(req.Signature, "\n ")
	if len(sig) > 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "签名过长（上限 2000 字）"})
		return
	}
	if err := h.st.SetMailSignature(sig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存签名失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---- 小工具 ----

func mailTarget(c *gin.Context) (folder string, uid int64, ok bool) {
	folder = c.Query("folder")
	if folder == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 folder"})
		return "", 0, false
	}
	if _, err := fmt.Sscanf(c.Query("uid"), "%d", &uid); err != nil || uid <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 uid"})
		return "", 0, false
	}
	return folder, uid, true
}

func rfc5987(s string) string {
	var b strings.Builder
	// 按 UTF-8 字节转义（filename* 声明的是字节序列）：按 rune 会把多字节
	// 字符输成 4+ 位十六进制，浏览器解出乱码
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c > 127 || c < 32 || strings.ContainsRune("\\\"/:;?#", rune(c)) {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}
