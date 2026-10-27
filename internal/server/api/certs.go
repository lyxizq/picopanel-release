package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/certcheck"
	"lz-monitor/internal/server/store"
)

// CertHandler HTTPS 证书监控清单的增删查与手动检查。
// 注：证书续签功能已整体下线（2026-10-19，TODO 25）。
type CertHandler struct {
	st *store.Store
}

type certView struct {
	store.Cert
	DaysLeft *int   `json:"days_left"` // 距到期天数（负=已过期；未查过为 null）
	State    string `json:"state"`     // ok/expiring/expired/error/unchecked
}

// toView 附加展示字段：剩余天数与状态。
func toView(c store.Cert) certView {
	v := certView{Cert: c}
	switch {
	case c.LastError != "":
		v.State = "error"
	case c.NotAfter == 0:
		v.State = "unchecked"
	default:
		d := int((c.NotAfter - time.Now().Unix()) / 86400)
		v.DaysLeft = &d
		switch {
		case d < 0:
			v.State = "expired"
		case d <= certcheck.RemindDays:
			v.State = "expiring"
		default:
			v.State = "ok"
		}
	}
	return v
}

// List 全量证书。
func (h *CertHandler) List(c *gin.Context) {
	certs, err := h.st.ListCerts()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询证书失败"})
		return
	}
	out := make([]certView, len(certs))
	for i := range certs {
		out[i] = toView(certs[i])
	}
	c.JSON(http.StatusOK, gin.H{"certs": out})
}

type createCertReq struct {
	Domain string `json:"domain" binding:"required"`
	Port   int    `json:"port"`
}

// Create 新增监控域名（默认 443 端口）；入库后立即查一轮拿首份结果。
func (h *CertHandler) Create(c *gin.Context) {
	var req createCertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "域名不能为空"})
		return
	}
	domain := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(req.Domain), "https://"))
	domain = strings.TrimSuffix(domain, "/")
	if domain == "" || strings.Contains(domain, "/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "域名格式不正确"})
		return
	}
	port := req.Port
	if port == 0 {
		port = 443
	}
	id, err := h.st.CreateCert(domain, port)
	if err != nil {
		if errors.Is(err, store.ErrCertExists) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "该域名已存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	// 新增即查（同步，前端加完直接看到结果；失败也不阻塞创建）
	cert := store.Cert{ID: id, Domain: domain, Port: port}
	certcheck.CheckOne(h.st, &cert)
	c.JSON(http.StatusOK, gin.H{"cert": toView(cert)})
}

// Delete 删除监控。
func (h *CertHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := h.st.DeleteCert(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Check 手动触发一轮全量检查（同步执行，域名多时可能要几秒）。
func (h *CertHandler) Check(c *gin.Context) {
	certcheck.CheckAll(h.st)
	h.List(c)
}
