package api

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/dbtool"
	"lz-monitor/internal/server/store"
)

// DBToolHandler 数据库管理工具接口（323-325，登录组）：连接 CRUD/测试、
// 结构浏览、数据行增删改查、SQL 控制台、备份（导出/清单/下载/删除）与导入。
type DBToolHandler struct {
	st     *store.Store
	mg     *dbtool.Manager
	dbPath string // 面板自身主库路径（自指连接拒绝用）

	dumpMu sync.Map // connID → *sync.Mutex，同连接备份串行
}

// dbConnReq 连接增改请求（password 空=编辑沿用旧凭据；sqlite 只需 name/path）。
type dbConnReq struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	Host           string `json:"host"`
	Port           *int   `json:"port"`
	Database       string `json:"database"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	Readonly       *bool  `json:"readonly"`
	Enabled        *bool  `json:"enabled"`
	SSL            string `json:"ssl"`
	TimeoutSec     *int   `json:"timeout_s"`
	TunnelServerID *int64 `json:"tunnel_server_id"`
}

func (h *DBToolHandler) view(c *store.DBConnection) gin.H {
	// 用户名随清单解密回显（编辑弹窗用，343）；密码仍不外发
	user, _, _ := h.st.DBConnCredentials(c)
	return gin.H{"id": c.ID, "name": c.Name, "type": c.Type, "host": c.Host,
		"port": c.Port, "database": c.Database, "username": user, "has_auth": c.HasAuth,
		"readonly": c.Readonly, "enabled": c.Enabled, "sort": c.Sort,
		"params": c.Params, "last_ok_at": c.LastOKAt, "last_error": c.LastError}
}

// buildConn 请求 → 账户（校验类型/地址/凭据形状）。编辑态旧凭据明文由调用方
// 解密传入（oldUser/oldPass），支撑「只改用户名不改密码」的重封。
func buildConn(req *dbConnReq, old *store.DBConnection, oldUser, oldPass string) (*store.DBConnection, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Host = strings.TrimSpace(req.Host)
	req.Database = strings.TrimSpace(req.Database)
	req.Username = strings.TrimSpace(req.Username)
	if req.Name == "" {
		return nil, fmt.Errorf("请填写连接名称")
	}
	validType := false
	for _, t := range store.DBConnTypes {
		if req.Type == t {
			validType = true
			break
		}
	}
	if !validType {
		return nil, fmt.Errorf("不支持的数据库类型")
	}
	c := &store.DBConnection{Name: req.Name, Type: req.Type, Host: req.Host, Database: req.Database,
		Readonly: req.Readonly != nil && *req.Readonly, Enabled: true}
	if old != nil { // 编辑态：布尔未给沿用旧值
		if req.Readonly == nil {
			c.Readonly = old.Readonly
		}
		if req.Enabled == nil {
			c.Enabled = old.Enabled
		} else {
			c.Enabled = *req.Enabled
		}
	} else if req.Readonly != nil {
		c.Readonly = *req.Readonly
	}
	c.Params.SSL = "off"
	if req.SSL == "skip-verify" || req.SSL == "on" {
		c.Params.SSL = req.SSL
	}
	c.Params.TimeoutSec = 10
	if req.TimeoutSec != nil {
		if *req.TimeoutSec < 1 || *req.TimeoutSec > 60 {
			return nil, fmt.Errorf("连接超时须在 1~60 秒")
		}
		c.Params.TimeoutSec = *req.TimeoutSec
	}
	if req.TunnelServerID != nil && *req.TunnelServerID > 0 {
		if c.Type == "sqlite" {
			return nil, fmt.Errorf("SQLite 是本机文件，不需要 SSH 隧道")
		}
		c.Params.TunnelServerID = *req.TunnelServerID
	}

	switch c.Type {
	case "sqlite":
		if !filepath.IsAbs(c.Database) {
			return nil, fmt.Errorf("SQLite 需要库文件的绝对路径")
		}
	default:
		if c.Host == "" {
			return nil, fmt.Errorf("请填写主机地址")
		}
		c.Port = map[string]int{"mysql": 3306, "postgres": 5432}[c.Type]
		if req.Port != nil && *req.Port > 0 {
			if *req.Port > 65535 {
				return nil, fmt.Errorf("端口不合法")
			}
			c.Port = *req.Port
		}
		if c.Type == "postgres" && c.Database == "" {
			return nil, fmt.Errorf("PostgreSQL 需要指定数据库名")
		}
		if req.Username == "" || (old == nil && req.Password == "") {
			return nil, fmt.Errorf("请填写用户名和密码")
		}
	}
	// 凭据：sqlite 无凭据；其余新密码优先、编辑未给沿用旧值
	switch {
	case c.Type == "sqlite":
		if old != nil {
			c.Auth = old.Auth
		}
	case old != nil && req.Password == "":
		if req.Username != "" && req.Username != oldUser {
			// 只改用户名不改密码：新用户名配旧密码重封（343——原整份沿用旧凭据
			// 会把新填的用户名静默丢掉）
			raw, _ := json.Marshal(map[string]string{"username": req.Username, "password": oldPass})
			c.Auth = string(raw)
		} else {
			c.Auth = old.Auth
		}
	default:
		if req.Username == "" {
			return nil, fmt.Errorf("请填写用户名")
		}
		raw, _ := json.Marshal(map[string]string{"username": req.Username, "password": req.Password})
		c.Auth = string(raw)
	}
	return c, nil
}

// rejectSelfDB SQLite 连接不得指向面板自身主库：只读浏览/控制台也会把 users
// 口令哈希、db_connections 密文、settings 全库暴露给拿到管理员会话的人，
// 比应有的边界宽。命中返回 true（响应已写）。
func (h *DBToolHandler) rejectSelfDB(c *gin.Context, conn *store.DBConnection) bool {
	if conn != nil && conn.Type == "sqlite" && h.dbPath != "" &&
		filepath.Clean(conn.Database) == filepath.Clean(h.dbPath) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该路径是面板主数据库，禁止添加为外部连接"})
		return true
	}
	return false
}

// checkIdent 表名/schema 入口预检：空串与 NUL 在方言 Quote 里是 panic 面，
// 用户可控输入必须在 API 层转 400 而不是打进 500。非法返回 true（响应已写）。
func checkIdent(c *gin.Context, s string) bool {
	if !dbtool.ValidIdent(s) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标识符无效"})
		return true
	}
	return false
}

func (h *DBToolHandler) list(c *gin.Context) {
	conns, err := h.st.ListDBConnections()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询连接失败"})
		return
	}
	out := make([]gin.H, 0, len(conns))
	for i := range conns {
		out = append(out, h.view(&conns[i]))
	}
	c.JSON(http.StatusOK, gin.H{"connections": out, "max": store.MaxDBConnections})
}

func (h *DBToolHandler) create(c *gin.Context) {
	var req dbConnReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	conns, _ := h.st.ListDBConnections()
	if len(conns) >= store.MaxDBConnections {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("最多 %d 个数据库连接", store.MaxDBConnections)})
		return
	}
	conn, err := buildConn(&req, nil, "", "")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.rejectSelfDB(c, conn) {
		return
	}
	// 先测后存（同邮件口径）：连不上不给存
	if err := h.testConn(c.Request.Context(), conn, req.Password); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "连接测试失败: " + err.Error()})
		return
	}
	id, err := h.st.CreateDBConnection(conn)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "id": id})
}

func (h *DBToolHandler) connOf(c *gin.Context) *store.DBConnection {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return nil
	}
	conn, err := h.st.GetDBConnection(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "数据库连接不存在"})
		return nil
	}
	return conn
}

// dial 建连 + 方言 + 超时的公共样板（原 handlers 里八处复制粘贴收敛）。
// timeout<=0 不包超时（沿用请求 ctx）；失败已写响应（建连 502 / 方言 400），
// recordFail=true 时建连失败顺带记一次健康态。调用方 defer cancel() 后判 ok。
func (h *DBToolHandler) dial(c *gin.Context, conn *store.DBConnection, timeout time.Duration, recordFail bool) (*sql.DB, dbtool.Dialect, context.Context, context.CancelFunc, bool) {
	db, _, err := h.mg.Conn(c.Request.Context(), conn.ID)
	if err != nil {
		if recordFail {
			_ = h.st.RecordDBConnStatus(conn.ID, false, err.Error())
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return nil, nil, nil, nil, false
	}
	d, err := dbtool.DialectFor(conn.Type)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, nil, nil, nil, false
	}
	var ctx context.Context
	cancel := context.CancelFunc(func() {})
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(c.Request.Context(), timeout)
	} else {
		ctx, cancel = context.WithCancel(c.Request.Context())
	}
	return db, d, ctx, cancel, true
}

func (h *DBToolHandler) update(c *gin.Context) {
	old := h.connOf(c)
	if old == nil {
		return
	}
	var req dbConnReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	// 旧凭据先解密（buildConn 只改用户名时需要旧密码重封）
	oldUser, oldPass, err := h.st.DBConnCredentials(old)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取凭据失败"})
		return
	}
	conn, err := buildConn(&req, old, oldUser, oldPass)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.rejectSelfDB(c, conn) {
		return
	}
	conn.ID = old.ID
	// 先测后存：用最终凭据（新密码或库存密码）测连通性
	user, pass, err := h.st.DBConnCredentials(conn)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取凭据失败"})
		return
	}
	if err := h.testConn(c.Request.Context(), conn, pass); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "连接测试失败: " + err.Error()})
		return
	}
	_ = user
	if err := h.st.UpdateDBConnection(conn); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	h.mg.Drop(conn.ID) // 配置指纹变了会自动重建，这里显式丢弃隧道与旧池
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

func (h *DBToolHandler) remove(c *gin.Context) {
	conn := h.connOf(c)
	if conn == nil {
		return
	}
	if err := h.st.DeleteDBConnection(conn.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	h.mg.Drop(conn.ID)
	h.mg.DeleteConnBackups(conn.ID)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// testConn 存库前的连通性测试（Test 内部新建临时连接，不进池）。
func (h *DBToolHandler) testConn(ctx context.Context, conn *store.DBConnection, passOverride string) error {
	user, pass, err := h.st.DBConnCredentials(conn)
	if err != nil {
		return err
	}
	if passOverride != "" {
		pass = passOverride
	}
	return h.mg.Test(ctx, conn, user, pass)
}

// testStored 已存连接的手动测试（可带新密码试连，编辑弹窗用）。
func (h *DBToolHandler) testStored(c *gin.Context) {
	conn := h.connOf(c)
	if conn == nil {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := h.testConn(c.Request.Context(), conn, strings.TrimSpace(body.Password)); err != nil {
		_ = h.st.RecordDBConnStatus(conn.ID, false, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	_ = h.st.RecordDBConnStatus(conn.ID, true, "")
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// testInput 未入库配置的测试（新建弹窗「测试连接」按钮）。
func (h *DBToolHandler) testInput(c *gin.Context) {
	var req dbConnReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	conn, err := buildConn(&req, nil, "", "")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.rejectSelfDB(c, conn) {
		return
	}
	if err := h.testConn(c.Request.Context(), conn, req.Password); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---- 结构浏览 ----

func (h *DBToolHandler) schemas(c *gin.Context) {
	conn := h.connOf(c)
	if conn == nil {
		return
	}
	db, d, ctx, cancel, ok := h.dial(c, conn, 15*time.Second, true)
	if !ok {
		return
	}
	defer cancel()
	schemas, err := d.Schemas(ctx, db)
	if err != nil {
		_ = h.st.RecordDBConnStatus(conn.ID, false, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	_ = h.st.RecordDBConnStatus(conn.ID, true, "")
	c.JSON(http.StatusOK, gin.H{"schemas": schemas})
}

// qschema 归一 schema 参数。
func qschema(conn *store.DBConnection, s string) string {
	s = strings.TrimSpace(s)
	if s != "" || conn.Type == "sqlite" {
		if conn.Type == "sqlite" {
			return "main"
		}
		return s
	}
	if conn.Type == "postgres" {
		return "public"
	}
	return conn.Database
}

func (h *DBToolHandler) tables(c *gin.Context) {
	conn := h.connOf(c)
	if conn == nil {
		return
	}
	db, d, ctx, cancel, ok := h.dial(c, conn, 15*time.Second, false)
	if !ok {
		return
	}
	defer cancel()
	ts, err := d.Tables(ctx, db, qschema(conn, c.Query("schema")))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	_ = h.st.RecordDBConnStatus(conn.ID, true, "")
	c.JSON(http.StatusOK, gin.H{"tables": ts})
}

func (h *DBToolHandler) table(c *gin.Context) {
	conn := h.connOf(c)
	if conn == nil {
		return
	}
	name := strings.TrimSpace(c.Query("name"))
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少表名"})
		return
	}
	if checkIdent(c, name) {
		return
	}
	db, d, ctx, cancel, ok := h.dial(c, conn, 20*time.Second, false)
	if !ok {
		return
	}
	defer cancel()
	det, err := d.Table(ctx, db, qschema(conn, c.Query("schema")), name)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	// 无主键表/视图的 PK 在方言里是 nil，序列化成 null 会让前端
	// detail.pk.length 炸渲染（346）——归一为空数组
	if det.PK == nil {
		det.PK = []string{}
	}
	c.JSON(http.StatusOK, det)
}

// ---- 数据行 ----

// columns 全库列元数据（SQL 编辑器补全用）：表名→列名清单，含视图。
func (h *DBToolHandler) columns(c *gin.Context) {
	conn := h.connOf(c)
	if conn == nil {
		return
	}
	db, d, ctx, cancel, ok := h.dial(c, conn, 15*time.Second, false)
	if !ok {
		return
	}
	defer cancel()
	cols, err := d.Columns(ctx, db, qschema(conn, c.Query("schema")))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"columns": cols})
}

func (h *DBToolHandler) rows(c *gin.Context) {
	conn := h.connOf(c)
	if conn == nil {
		return
	}
	table := strings.TrimSpace(c.Query("table"))
	if table == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少表名"})
		return
	}
	if checkIdent(c, table) {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
	var filters []dbtool.Filter
	if f := c.Query("filters"); f != "" {
		if err := json.Unmarshal([]byte(f), &filters); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "filters 参数不合法"})
			return
		}
	}
	db, d, ctx, cancel, ok := h.dial(c, conn, 20*time.Second, false)
	if !ok {
		return
	}
	defer cancel()
	res, err := dbtool.PageRows(ctx, db, d, qschema(conn, c.Query("schema")), table,
		filters, c.Query("sort"), c.Query("dir"), page, size)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

type rowWriteReq struct {
	Schema string         `json:"schema"`
	Table  string         `json:"table"`
	PK     map[string]any `json:"pk"`
	Values map[string]any `json:"values"`
}

func (h *DBToolHandler) guardWrite(c *gin.Context, conn *store.DBConnection) bool {
	// 2026-10-03 用户要求全工具仅查询（TODO 330）：行级增删改与 SQL 导入一律
	// 拒绝，功能代码保留；恢复写入时改回按 conn.Readonly 判断。
	_ = conn
	c.JSON(http.StatusBadRequest, gin.H{"error": "此工具仅对数据库进行查询，无写入操作"})
	return false
}

func (h *DBToolHandler) insertRow(c *gin.Context) {
	h.rowWrite(c, func(ctx context.Context, conn *store.DBConnection, db *sql.DB, d dbtool.Dialect, req *rowWriteReq) (any, error) {
		n, id, err := dbtool.InsertRow(ctx, db, d, qschema(conn, req.Schema), req.Table, req.Values)
		if err != nil {
			return nil, err
		}
		return gin.H{"affected": n, "last_id": id}, nil
	})
}

func (h *DBToolHandler) updateRow(c *gin.Context) {
	h.rowWrite(c, func(ctx context.Context, conn *store.DBConnection, db *sql.DB, d dbtool.Dialect, req *rowWriteReq) (any, error) {
		n, err := dbtool.UpdateRow(ctx, db, d, qschema(conn, req.Schema), req.Table, req.PK, req.Values)
		if err != nil {
			return nil, err
		}
		return gin.H{"affected": n}, nil
	})
}

func (h *DBToolHandler) deleteRow(c *gin.Context) {
	h.rowWrite(c, func(ctx context.Context, conn *store.DBConnection, db *sql.DB, d dbtool.Dialect, req *rowWriteReq) (any, error) {
		n, err := dbtool.DeleteRow(ctx, db, d, qschema(conn, req.Schema), req.Table, req.PK)
		if err != nil {
			return nil, err
		}
		return gin.H{"affected": n}, nil
	})
}

func (h *DBToolHandler) rowWrite(c *gin.Context, fn func(ctx context.Context, conn *store.DBConnection, db *sql.DB, d dbtool.Dialect, req *rowWriteReq) (any, error)) {
	conn := h.connOf(c)
	if conn == nil {
		return
	}
	if !h.guardWrite(c, conn) {
		return
	}
	var req rowWriteReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Table == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if checkIdent(c, req.Table) || (strings.TrimSpace(req.Schema) != "" && checkIdent(c, strings.TrimSpace(req.Schema))) {
		return
	}
	db, d, ctx, cancel, ok := h.dial(c, conn, 20*time.Second, false)
	if !ok {
		return
	}
	defer cancel()
	out, err := fn(ctx, conn, db, d, &req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	_ = h.st.RecordDBConnStatus(conn.ID, true, "")
	c.JSON(http.StatusOK, out)
}

// ---- SQL 控制台 ----

func (h *DBToolHandler) query(c *gin.Context) {
	conn := h.connOf(c)
	if conn == nil {
		return
	}
	var req struct {
		SQL     string `json:"sql"`
		Schema  string `json:"schema"` // 库上下文（空=连接默认；控制台库下拉回传）
		Confirm bool   `json:"confirm"`
		MaxRows int    `json:"max_rows"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.SQL) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if schema := strings.TrimSpace(req.Schema); schema != "" && checkIdent(c, schema) {
		return
	}
	db, d, _, cancel, ok := h.dial(c, conn, 0, true)
	if !ok {
		return
	}
	defer cancel()	// 只读位硬编码 true（2026-10-03 TODO 330，与 guardWrite 同批）：控制台仅
	// 放行查询语句；恢复写入时改回 conn.Readonly。
	res, err := dbtool.RunConsole(c.Request.Context(), db, d, true, req.SQL, req.MaxRows, req.Confirm, strings.TrimSpace(req.Schema))
	if err != nil {
		if errors.Is(err, dbtool.ErrReadOnly) { // 只读位已硬编码，报真实口径而非误导性的"连接为只读"
			err = fmt.Errorf("此工具仅对数据库进行查询，无写入操作")
		}
		_ = h.st.RecordDBConnStatus(conn.ID, false, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	_ = h.st.RecordDBConnStatus(conn.ID, true, "")
	c.JSON(http.StatusOK, res)
}

// ---- 备份与导入 ----

func (h *DBToolHandler) backups(c *gin.Context) {
	conn := h.connOf(c)
	if conn == nil {
		return
	}
	files, err := h.mg.ListBackups(conn.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取备份目录失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"files": files})
}

func (h *DBToolHandler) backupCreate(c *gin.Context) {
	conn := h.connOf(c)
	if conn == nil {
		return
	}
	var req struct {
		Schema string   `json:"schema"`
		Tables []string `json:"tables"`
	}
	_ = c.ShouldBindJSON(&req)
	muAny, _ := h.dumpMu.LoadOrStore(conn.ID, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	if !mu.TryLock() {
		c.JSON(http.StatusConflict, gin.H{"error": "该连接已有备份在进行中，请等它完成"})
		return
	}
	defer mu.Unlock()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Minute)
	defer cancel()
	f, err := h.mg.CreateBackup(ctx, conn.ID, req.Schema, req.Tables)
	if err != nil {
		_ = h.st.RecordDBConnStatus(conn.ID, false, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"error": "备份失败: " + err.Error()})
		return
	}
	_ = h.st.RecordDBConnStatus(conn.ID, true, "")
	c.JSON(http.StatusOK, gin.H{"code": 0, "file": f})
}

func (h *DBToolHandler) backupDelete(c *gin.Context) {
	conn := h.connOf(c)
	if conn == nil {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := h.mg.DeleteBackup(conn.ID, req.Name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// backupDownload 备份文件下载（挂免 gzip 组；文件 0600 落盘，传输走登录鉴权）。
func (h *DBToolHandler) backupDownload(c *gin.Context) {
	id, err := strconv.ParseInt(c.Query("conn"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if _, err := h.st.GetDBConnection(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "数据库连接不存在"})
		return
	}
	p, err := h.mg.BackupPath(id, c.Query("name"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	f, err := os.Open(p)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "备份文件不存在"})
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", rfc5987(filepath.Base(p))))
	c.Header("Content-Length", strconv.FormatInt(st.Size(), 10))
	c.DataFromReader(http.StatusOK, st.Size(), "application/gzip", f, nil)
}

// importSQL 导入 SQL 文件（.sql 或 .sql.gz，multipart 字段 file）。
func (h *DBToolHandler) importSQL(c *gin.Context) {
	conn := h.connOf(c)
	if conn == nil {
		return
	}
	if !h.guardWrite(c, conn) {
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少导入文件"})
		return
	}
	const maxImport = 256 << 20 // 256MB 解压后上限
	src, err := fh.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件读取失败"})
		return
	}
	defer src.Close()
	var r io.Reader = io.LimitReader(src, maxImport)
	if strings.HasSuffix(strings.ToLower(fh.Filename), ".gz") {
		gz, gerr := gzip.NewReader(r)
		if gerr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "gzip 文件无效"})
			return
		}
		defer gz.Close()
		r = io.LimitReader(gz, maxImport)
	}
	content, err := io.ReadAll(r)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件读取失败"})
		return
	}
	db, d, ctx, cancel, ok := h.dial(c, conn, 30*time.Minute, false)
	if !ok {
		return
	}
	defer cancel()
	n, err := dbtool.Import(ctx, db, d, string(content), nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "executed": n})
		return
	}
	_ = h.st.RecordDBConnStatus(conn.ID, true, "")
	c.JSON(http.StatusOK, gin.H{"code": 0, "executed": n})
}
