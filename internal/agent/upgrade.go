// upgrade.go 实现 agent 在线升级（TODO 396）：pull 模式经指标端口收服务端
// 直推的分发束（POST /upgrade），push 模式从上报响应领指令后回源下载。
// 束内容统一为 manifest.json + 签名 + 更新包三段：验签（内嵌公钥）→
// 按 manifest 校验 sha256 → 解压试运行 → 原子替换 → exec 同 PID 重启，
// 启动失败由 main 凭升级标记自动回滚（internal/update 共用链路）。
package agent

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime"
	"time"

	"lz-monitor/internal/update"
)

// Runtime 是构建注入的运行期信息（版本串 + 验签公钥），main 装配传入。
type Runtime struct {
	Version string // 构建版本串（git describe），随 Sys 上报
	PubKey  string // 发布验签公钥 hex（deploy/picopanel.pub，构建注入）
}

// maxUpgradeBody 分发束大小上限（agent 包 gz 约 4MB，余量防未来膨胀）。
const maxUpgradeBody = 64 << 20

// UpgradeHandler 处理 pull 模式的升级推送（POST /upgrade）：仅服务端
// （持 X-Server-Token）可调；agent 未配置 token 时一律拒绝——二进制替换
// 等于任意代码执行，绝不给无鉴权端口开这扇门。
type UpgradeHandler struct {
	cfg *Config
	rt  Runtime
}

func (h *UpgradeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.UpgradeEnabled() {
		http.Error(w, "本机配置已关闭在线升级（agent.yaml upgrade: false）", http.StatusForbidden)
		return
	}
	if h.cfg.Token == "" {
		// 安全红线：无鉴权的指标端口绝不接受升级指令
		http.Error(w, "本机未配置抓取密钥（token），拒绝在线升级；请在 agent.yaml 配置 token 后重试", http.StatusForbidden)
		return
	}
	got := r.Header.Get("X-Server-Token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.cfg.Token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := h.applyBundle(r.Body, r.Header.Get("Content-Type")); err != nil {
		log.Printf("在线升级被拒绝: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Apply 成功 = exec 已换映像，本行不会执行到；防御性输出
	w.Write([]byte("ok"))
}

// applyBundle 验束 → 验包 → 替换重启（pull/push 两路共用）。
func applyBundle(body io.Reader, contentType string, rt Runtime) error {
	mr, err := update.NewBundleReader(contentType, body)
	if err != nil {
		return err
	}
	manifestRaw, sig, pkg, err := update.ParseBundle(mr, maxUpgradeBody)
	if err != nil {
		return err
	}
	if err := update.VerifyManifest(rt.PubKey, manifestRaw, sig); err != nil {
		return err
	}
	m, err := update.ParseManifest(manifestRaw)
	if err != nil {
		return err
	}
	if !update.Newer(rt.Version, m.Version) {
		return errors.New("目标版本不比本机新，无需升级")
	}
	if err := update.Fresh(m, time.Now()); err != nil {
		return err
	}
	asset, ok := m.Assets[update.AssetAgentAmd]
	if runtime.GOARCH == "arm64" {
		asset, ok = m.Assets[update.AssetAgentArm]
	}
	if !ok {
		return fmt.Errorf("manifest 缺本机架构（%s）的更新包", runtime.GOARCH)
	}
	log.Printf("收到在线升级指令：%s → %s，开始应用", rt.Version, m.Version)
	return update.Apply(update.ApplyOptions{
		PackageGZ:    pkg,
		ExpectSHA256: asset.SHA256,
		CheckArgs:    []string{"-check-config"},
		FromVersion:  rt.Version,
	})
}

func (h *UpgradeHandler) applyBundle(body io.Reader, contentType string) error {
	return applyBundle(body, contentType, h.rt)
}

// upgradeSelf push 模式回源下载分发束并应用（凭 X-Agent-Token 身份）。
func upgradeSelf(rep *Reporter, hint string, rt Runtime) error {
	if rt.PubKey == "" {
		return errors.New("构建未注入验签公钥，无法安全升级")
	}
	body, ct, err := rep.FetchBundle(runtime.GOARCH)
	if err != nil {
		return err
	}
	log.Printf("收到在线升级指令：%s，开始应用", hint)
	return applyBundle(bytes.NewReader(body), ct, rt)
}
