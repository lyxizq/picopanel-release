// Package update 实现在线更新核心链路（TODO 395/396，server 与 agent 共用）：
// manifest 解析（schema 卡点）→ ed25519 验签 → 下载（sha256 校验）→ 解压到
// 临时文件试运行（-check-config）→ 磁盘预检 → 原子替换（旧版保 .old）→
// 写升级标记 → syscall.Exec 同 PID 重启。启动侧凭标记做健康自检：初始化成功
// 清标记；失败由 main 调 Rollback 自动 exec .old 回滚（防呆：至多回滚一次，
// 旧版也起不来时直接退出，绝不无限循环）。
//
// 跨任意版本安全（395⑧）：本链路不关心版本号语义；数据安全靠调用方钩子
// （升级前 db 备份）与 store 迁移纪律（user_version 顺序链），协议卡点靠
// ParseManifest 的 schema 校验——不认识的 schema 直接拒绝。
package update

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// logf 统一打前缀，两端日志里可辨认更新器输出。
func logf(format string, args ...any) {
	log.Printf("[update] "+format, args...)
}

// ManifestSchema 是当前客户端认识的 manifest 协议版本。发布侧（make channel）
// 写 schema:1；未来结构大改时递增，旧客户端 ParseManifest 报 ErrUnsupportedSchema，
// 拒绝自动升级（提示手动下载）——跨大版本不被新 manifest 搞坏的卡点。
const ManifestSchema = 1

// ErrUnsupportedSchema 表示 manifest 的 schema 高于本二进制认识的范围。
var ErrUnsupportedSchema = errors.New("manifest schema 不受支持，请手动下载新版本安装包升级")

// Asset 键名与发布侧 channel.sh 的 manifest 保持一致。
const (
	AssetServer    = "server"
	AssetAgentAmd  = "agent_amd64"
	AssetAgentArm  = "agent_arm64"
	fileServer     = "lz-server.gz"
	fileAgentAmd   = "lz-agent-amd64.gz"
	fileAgentArm   = "lz-agent-arm64.gz"
	manifestName   = "manifest.json"
	manifestSig    = "manifest.json.sig"
	maxPackageSize = 256 << 20 // 单包下载/解包上限（服务端包 gz 约 21MB，余量给未来）
)

// AssetFile 返回 asset 键对应的发行文件名；未知键报错。
func AssetFile(key string) (string, error) {
	switch key {
	case AssetServer:
		return fileServer, nil
	case AssetAgentAmd:
		return fileAgentAmd, nil
	case AssetAgentArm:
		return fileAgentArm, nil
	}
	return "", fmt.Errorf("未知资产类型 %q", key)
}

// Asset 是 manifest 里一个发行包的校验信息。
type Asset struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest 是更新通道的版本指针（发布时由 deploy/channel.sh 生成并签名）。
type Manifest struct {
	Schema      int              `json:"schema"`
	Version     string           `json:"version"`
	Channel     string           `json:"channel"`
	PublishedAt string           `json:"published_at"`
	Notes       string           `json:"notes"`
	Assets      map[string]Asset `json:"assets"`
}

// ParseManifest 解析并做 schema 卡点校验。
func ParseManifest(raw []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("manifest 解析失败: %w", err)
	}
	if m.Schema != ManifestSchema {
		return nil, fmt.Errorf("%w（manifest schema=%d，本程序支持 %d）", ErrUnsupportedSchema, m.Schema, ManifestSchema)
	}
	if m.Version == "" || len(m.Assets) == 0 {
		return nil, errors.New("manifest 缺 version 或 assets")
	}
	return &m, nil
}

// VerifyManifest 用内嵌公钥（构建注入）验 manifest 签名。
func VerifyManifest(pubHex string, manifestRaw []byte, sigHex string) error {
	pub, err := hex.DecodeString(strings.TrimSpace(pubHex))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("验签公钥无效（构建未注入发布公钥？）")
	}
	sig, err := hex.DecodeString(strings.TrimSpace(sigHex))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("签名格式无效")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), manifestRaw, sig) {
		return errors.New("manifest 签名验证失败——内容与发布方不匹配")
	}
	return nil
}

// VerifyAsset 校验下载包的 sha256（与 manifest.assets 条目比对）。
func VerifyAsset(a Asset, pkg []byte) error {
	sum := sha256.Sum256(pkg)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, a.SHA256) {
		return fmt.Errorf("sha256 不匹配（期望 %s，实际 %s）——下载不完整或被篡改", a.SHA256, got)
	}
	if a.Size > 0 && int64(len(pkg)) != a.Size {
		return fmt.Errorf("包大小不符（期望 %d，实际 %d）", a.Size, len(pkg))
	}
	return nil
}

// parseVersion 解析版本串为数字段 + 「tag 后提交数」。认识 git describe 全形态：
//   - "v1.2.3"          → ([1,2,3], 0, true)
//   - "v1.2.3-2-g5a9773" → ([1,2,3], 2, true)（比 v1.2.3 新 2 个提交）
//   - "v1.2.3-dirty"     → ([1,2,3], 0, true)（本地有改动，提交数未知按 0）
//
// 完全不可解析（纯短哈希/dev）时 ok=false。
func parseVersion(v string) ([]int, int, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return nil, 0, false
	}
	ahead := 0
	if i := strings.Index(v, "-"); i >= 0 {
		suffix, base := v[i+1:], v[:i]
		v = base
		fields := strings.Split(suffix, "-")
		if n, err := strconv.Atoi(fields[0]); err == nil && len(fields) >= 2 && strings.HasPrefix(fields[1], "g") {
			ahead = n // git describe 的 -N-g<hash>
		}
	}
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, 0, false
		}
		out[i] = n
	}
	return out, ahead, true
}

// compareParts 数字段逐段比较（缺段补 0）：1= a 新，-1= b 新，0= 同基线。
func compareParts(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if y > x {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Newer 判断 latest 是否比 cur 新（可升级）。语义：
//   - 双方都是语义版本：按数字逐段比较（缺段补 0）；
//   - cur 是 git describe 形态（如 v1.2.3-2-gabc = 比 v1.2.3 新 2 个提交）：
//     以其 tag 基线参与比较——通道不高于基线、或基线相同且 cur 领先时，
//     当前版本已不低于通道，不算可升级；
//   - cur 完全不可解析（dev/纯短哈希）：视为不可比，返回 false——dev 构建不
//     自动升级（防通道重放旧签名的版本实现降级），要升级请用正式构建。
func Newer(cur, latest string) bool {
	if strings.TrimSpace(cur) == strings.TrimSpace(latest) {
		return false
	}
	cp, _, ok1 := parseVersion(cur) // curAhead（tag 后提交数）不参与落后判定：同基线即不算落后
	lp, _, ok2 := parseVersion(latest)
	if !ok1 || !ok2 {
		return false
	}
	switch compareParts(cp, lp) {
	case -1: // 通道基线高于当前基线
		return true
	case 1: // 当前基线已高于通道
		return false
	}
	return false // 同基线：无论 cur 是否带 tag 后提交（领先），都不算落后
}

// MaxReplayAge manifest 发布时间新鲜度窗口。签名只证明「内容出自发布方」，
// 不证明「不是旧的」——明文通道（agent 指标端口与 push 回源均为 http）上的
// 中间人可回放历史合法签名束，把两端钉在含已知漏洞的旧版（降级攻击）。以
// 本机时钟为锚拒绝陈旧 manifest：正常升级永远追最新发布（发布节奏远小于
// 30 天），回放价值随发布时间衰减归零。
const MaxReplayAge = 30 * 24 * time.Hour

// Fresh 校验 manifest 发布时间是否在新鲜度窗口内（防旧版重放降级）。
// PublishedAt 缺失/不可解析时放行——签名整体覆盖 manifest，攻击者无法剥掉
// 该字段再重签，缺失只可能出自异常历史版本，Newer 的版本比较仍是一道闸。
func Fresh(m *Manifest, now time.Time) error {
	if m == nil || m.PublishedAt == "" {
		return nil
	}
	pub, err := time.Parse(time.RFC3339, m.PublishedAt)
	if err != nil {
		return nil
	}
	if d := now.Sub(pub); d > MaxReplayAge {
		return fmt.Errorf("manifest 发布于 %s（超过 %d 天新鲜度窗口），拒绝应用（防旧版重放降级）",
			pub.Format("2006-01-02"), int(MaxReplayAge.Hours()/24))
	}
	return nil
}

// Resolved 是一次成功探测的结果：验签后的 manifest 原文/签名 + 取资产的地址。
type Resolved struct {
	Manifest *Manifest
	Raw      []byte
	Sig      string
	// AssetURL 返回某发行文件（AssetFile 的返回值）的下载地址。
	AssetURL func(file string) (string, error)
}

// ResolveLatest 依次尝试 sources（首个成功即返回），按 channel 选版本：
//   - 静态源（http(s):// 开头的目录地址）：GET <src>/manifest.json + .sig；
//     stable/beta 由目录本身区分（如 …/stable 与 …/beta）。
//   - GitHub 源（"github:owner/repo"）：stable=releases/latest（自动排除
//     prerelease/draft），beta=最新一个非 draft Release（含 prerelease）。
func ResolveLatest(ctx context.Context, cli *http.Client, sources []string, channel string) (*Resolved, error) {
	if len(sources) == 0 {
		return nil, errors.New("未配置更新源（update.sources）")
	}
	var lastErr error
	for _, src := range sources {
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		var r *Resolved
		var err error
		if rest, ok := strings.CutPrefix(src, "github:"); ok {
			r, err = resolveGitHub(ctx, cli, strings.Trim(rest, "/"), channel)
		} else {
			r, err = resolveStatic(ctx, cli, strings.TrimRight(src, "/"))
		}
		if err == nil {
			return r, nil
		}
		lastErr = fmt.Errorf("源 %s: %w", src, err)
	}
	if lastErr == nil {
		lastErr = errors.New("更新源列表为空")
	}
	return nil, lastErr
}

func fetch(ctx context.Context, cli *http.Client, url string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("响应体 %d 字节超过上限", resp.ContentLength)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxBytes {
		return nil, fmt.Errorf("响应体超过 %d 字节上限", maxBytes)
	}
	return b, nil
}

func resolveStatic(ctx context.Context, cli *http.Client, base string) (*Resolved, error) {
	raw, err := fetch(ctx, cli, base+"/"+manifestName, 1<<20)
	if err != nil {
		return nil, err
	}
	sigB, err := fetch(ctx, cli, base+"/"+manifestSig, 4096)
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(raw)
	if err != nil {
		return nil, err
	}
	return &Resolved{Manifest: m, Raw: raw, Sig: string(sigB),
		AssetURL: func(file string) (string, error) {
			return base + "/" + url.PathEscape(file), nil
		}}, nil
}

// githubAPI GitHub API 基址（测试可指向本地假服务）。
var githubAPI = "https://api.github.com"

func resolveGitHub(ctx context.Context, cli *http.Client, ownerRepo, channel string) (*Resolved, error) {
	if ownerRepo == "" || !strings.Contains(ownerRepo, "/") {
		return nil, errors.New("github 源应写 github:owner/repo")
	}
	api := githubAPI + "/repos/" + ownerRepo
	var rel struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
		Assets     []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if channel == "beta" {
		var rels []struct {
			TagName    string `json:"tag_name"`
			Prerelease bool   `json:"prerelease"`
			Draft      bool   `json:"draft"`
			Assets     []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			} `json:"assets"`
		}
		b, err := fetch(ctx, cli, api+"/releases?per_page=20", 4<<20)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &rels); err != nil {
			return nil, fmt.Errorf("解析 releases: %w", err)
		}
		for _, r := range rels { // 最新在前；跳过 draft，prerelease 即 beta
			if r.Draft {
				continue
			}
			rel.TagName, rel.Prerelease, rel.Assets = r.TagName, r.Prerelease, r.Assets
			break
		}
		if rel.TagName == "" {
			return nil, errors.New("beta 通道没有可用 Release")
		}
	} else {
		b, err := fetch(ctx, cli, api+"/releases/latest", 1<<20)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &rel); err != nil {
			return nil, fmt.Errorf("解析 latest: %w", err)
		}
		if rel.TagName == "" {
			return nil, errors.New("stable 通道没有可用 Release")
		}
	}
	urls := map[string]string{}
	for _, a := range rel.Assets {
		urls[a.Name] = a.BrowserDownloadURL
	}
	get := func(name string) (string, error) {
		if u, ok := urls[name]; ok {
			return u, nil
		}
		return "", fmt.Errorf("Release %s 缺资产 %s", rel.TagName, name)
	}
	rawURL, err := get(manifestName)
	if err != nil {
		return nil, err
	}
	raw, err := fetch(ctx, cli, rawURL, 1<<20)
	if err != nil {
		return nil, err
	}
	sigURL, err := get(manifestSig)
	if err != nil {
		return nil, err
	}
	sigB, err := fetch(ctx, cli, sigURL, 4096)
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(raw)
	if err != nil {
		return nil, err
	}
	return &Resolved{Manifest: m, Raw: raw, Sig: string(sigB), AssetURL: get}, nil
}

// DownloadAsset 从 Resolved 取一个资产并做 manifest 校验（签名已验过，这里
// 对 sha256/大小把关）。
func DownloadAsset(ctx context.Context, cli *http.Client, r *Resolved, assetKey string) ([]byte, error) {
	asset, ok := r.Manifest.Assets[assetKey]
	if !ok {
		return nil, fmt.Errorf("manifest 缺资产 %s", assetKey)
	}
	file, err := AssetFile(assetKey)
	if err != nil {
		return nil, err
	}
	u, err := r.AssetURL(file)
	if err != nil {
		return nil, err
	}
	pkg, err := fetch(ctx, cli, u, maxPackageSize)
	if err != nil {
		return nil, fmt.Errorf("下载 %s: %w", file, err)
	}
	if err := VerifyAsset(asset, pkg); err != nil {
		return nil, err
	}
	return pkg, nil
}

// ApplyOptions 是一次自替换的全部输入。零值字段取合理缺省（可执行文件路径、
// 同目录 upgrade.json 标记、os.Args 重启参数、syscall.Exec）。
type ApplyOptions struct {
	PackageGZ    []byte                                 // 已验签下载、sha256 校验过的 .gz 包内容
	ExpectSHA256 string                                 // manifest 里的 sha256（发布侧通道校验过则可空）
	CheckArgs    []string                               // 试运行参数（两端均传 -check-config）；空=跳过试运行
	BinPath      string                                 // 目标二进制（缺省 os.Executable）
	MarkerPath   string                                 // 升级标记（缺省同目录 upgrade.json）
	FromVersion  string                                 // 当前版本（记进标记，排查用）
	PreRestart   func() error                           // 重启前钩子：server=停 http+checkpoint 关库；agent=无
	RestartArgs  []string                               // exec 重启参数（缺省 os.Args）
	Exec         func(path string, args []string) error // 可注入（测试用）
}

// Apply 执行替换并 exec 重启（成功时本调用不返回——进程映像已换）。
// 任何一步失败都发生在旧二进制仍完好的状态下（先解压临时名、先试运行、
// 最后一步才动原文件），旧版不受影响。
func Apply(o ApplyOptions) error {
	if len(o.PackageGZ) == 0 {
		return errors.New("更新包为空")
	}
	if o.ExpectSHA256 != "" {
		if err := VerifyAsset(Asset{SHA256: o.ExpectSHA256, Size: int64(len(o.PackageGZ))}, o.PackageGZ); err != nil {
			return err
		}
	}
	if o.BinPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("定位当前可执行文件: %w", err)
		}
		o.BinPath = exe
	}
	if o.MarkerPath == "" {
		o.MarkerPath = filepath.Join(filepath.Dir(o.BinPath), "upgrade.json")
	}
	if o.RestartArgs == nil {
		o.RestartArgs = os.Args
	}
	if o.Exec == nil {
		o.Exec = execFn
	}

	// 磁盘预检：解压后明文约 2.5×gz，加新旧并存与 .old 余量，按 gz×6 要空间
	if err := checkDisk(filepath.Dir(o.BinPath), int64(len(o.PackageGZ))*6); err != nil {
		return err
	}

	// 解压到随机临时名（先解压后替换：包损坏时旧版不受影响）。固定名
	// BinPath+".new" 在安装目录对非 root 可写时可被预置符号链接劫持——
	// O_TRUNC 写入会跟随链接落到任意目标，CreateTemp 的 O_EXCL 语义消除该面
	newBin, err := gunzipTo(o.PackageGZ, filepath.Dir(o.BinPath))
	if err != nil {
		return err
	}
	defer os.Remove(newBin) // exec 成功后进程已换，此行不再执行；失败路径清理

	// 试运行：新二进制 + 当前配置空载验证（-check-config），不过关中止保旧版
	if len(o.CheckArgs) > 0 {
		if err := trialRun(newBin, o.CheckArgs); err != nil {
			return fmt.Errorf("新版本试运行未通过（保留旧版）: %w", err)
		}
	}

	if o.PreRestart != nil {
		if err := o.PreRestart(); err != nil {
			return fmt.Errorf("重启前准备失败（保留旧版）: %w", err)
		}
	}

	oldBin := o.BinPath + ".old"
	_ = os.Remove(oldBin) // 上一轮的 .old 直接淘汰
	if err := os.Rename(o.BinPath, oldBin); err != nil {
		return fmt.Errorf("备份旧版失败: %w", err)
	}
	if err := os.Rename(newBin, o.BinPath); err != nil {
		// 换回旧版，绝不留在半替换状态
		_ = os.Rename(oldBin, o.BinPath)
		return fmt.Errorf("新版本就位失败: %w", err)
	}
	if err := os.Chmod(o.BinPath, 0o755); err != nil {
		_ = os.Rename(oldBin, o.BinPath)
		return fmt.Errorf("设置执行权限失败: %w", err)
	}
	syncDir(filepath.Dir(o.BinPath))
	if err := writeMarker(o.MarkerPath, &Marker{
		OldBin: oldBin, From: o.FromVersion, At: time.Now().Unix(),
	}); err != nil {
		// 标记是断电自动回滚的凭据：写不进去说明安装目录状态异常（磁盘满/
		// 权限被改），带病升级会在启动失败时无法回滚——换回旧版中止本次升级
		_ = os.Rename(o.BinPath, newBin)
		_ = os.Rename(oldBin, o.BinPath)
		return fmt.Errorf("写升级标记失败（已回退旧版，中止升级）: %w", err)
	}
	return o.Exec(o.BinPath, o.RestartArgs)
}

// execFn 进程自替换出口，测试可桩掉（真实场景 = syscall.Exec 同 PID 换映像）。
var execFn = execSelf

func execSelf(path string, args []string) error {
	return syscall.Exec(path, args, os.Environ())
}

// checkDisk 预检目录所在文件系统剩余空间（395⑧ 磁盘预检）。
func checkDisk(dir string, need int64) error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return nil // 读不到文件系统信息（极端平台）不阻断，让后续写入自然报错
	}
	free := int64(st.Bavail) * int64(st.Bsize)
	if free < need {
		return fmt.Errorf("磁盘剩余空间不足：需约 %dMB，仅剩 %dMB", need>>20, free>>20)
	}
	return nil
}

// gunzipTo 解压到 dir 下随机命名的新文件（CreateTemp，O_EXCL 创建不跟随
// 符号链接），写完 fsync 落盘后返回实际路径。两段 rename 之间本无二进制
// 窗口，fsync 把数据页与目录项一起收敛进断电安全边界。
func gunzipTo(pkg []byte, dir string) (string, error) {
	zr, err := gzip.NewReader(bytes.NewReader(pkg))
	if err != nil {
		return "", fmt.Errorf("解压更新包: %w", err)
	}
	defer zr.Close()
	f, err := os.CreateTemp(dir, ".upgrade-*.new")
	if err != nil {
		return "", err
	}
	dst := f.Name()
	n, err := io.Copy(f, zr)
	if err == nil {
		err = f.Chmod(0o755)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		os.Remove(dst)
		return "", fmt.Errorf("解压写入: %w", err)
	}
	if closeErr != nil {
		os.Remove(dst)
		return "", closeErr
	}
	if n < 1<<20 { // 明文二进制至少 1MB，截断/坏包在这里现形
		os.Remove(dst)
		return "", fmt.Errorf("解压结果仅 %d 字节，包疑似损坏", n)
	}
	return dst, nil
}

// syncDir fsync 目录项：两段 rename 之间断电可能出现「.old 在、正式名缺失」，
// 刷目录元数据把该窗口收敛到一次 fsync 内。
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

// trialRun 以 60s 超时跑一次新二进制的空载自检；退出码非 0 即失败。
func trialRun(bin string, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		trim := strings.TrimSpace(string(out))
		if len(trim) > 400 {
			trim = trim[:400] + "…"
		}
		return fmt.Errorf("%v: %s", err, trim)
	}
	return nil
}

// ── 升级标记（看门狗，395⑧ 健康自检 + 自动回滚）─────────────────────────

// Marker 记录一次待确认的升级：新二进制完成健康初始化后 Clear；初始化失败
// 由 main 调 Rollback exec 回旧版。Attempts 防死循环：回滚后旧版仍起不来
// （标记仍在且已试过一次）就直接退出报错，绝不来回 exec 打转。
type Marker struct {
	OldBin   string `json:"old_bin"`
	From     string `json:"from_version"`
	At       int64  `json:"at"`
	Attempts int    `json:"attempts"`
}

// DefaultMarkerPath 可执行文件同目录 upgrade.json。
func DefaultMarkerPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "upgrade.json"
	}
	return filepath.Join(filepath.Dir(exe), "upgrade.json")
}

func writeMarker(path string, m *Marker) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	// OldBin 路径会被回滚逻辑直接 exec：文件权限收紧到 0600，防同机其他用户
	// 改写标记把下次回滚指向恶意二进制
	return os.WriteFile(path, b, 0o600)
}

// LoadMarker 读取升级标记；不存在返回 nil。
func LoadMarker(path string) (*Marker, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m Marker
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("升级标记损坏: %w", err)
	}
	return &m, nil
}

// MarkerExists 标记是否在位（main 判定"本次启动处于升级后待确认期"）。
func MarkerExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// WriteVersionFile 把版本串写到 dir/VERSION（明文一行，2026-10-06 用户要求：
// 从磁盘文件即可知道服务端/agent 的当前版本）。内容不变时不重写——文件
// mtime 即最近一次换版本的升级时间。在线升级/手动替换二进制后新进程一启动
// 即刷新；agent 侧这是升级标记/日志之外唯一的常驻磁盘写入。
func WriteVersionFile(dir, version string) error {
	path := filepath.Join(dir, "VERSION")
	want := version + "\n"
	if b, err := os.ReadFile(path); err == nil && string(b) == want {
		return nil
	}
	return os.WriteFile(path, []byte(want), 0o644)
}

// ClearMarker 健康启动成功后调用：升级确认完成。
func ClearMarker(path string) {
	if err := os.Remove(path); err == nil {
		logf("升级确认完成（健康自检通过）")
	}
}

// Rollback 新版本启动失败时回滚：exec 旧版（标记 Attempts+1 防打转）。
// 已回滚过一次仍失败、或标记缺失/旧版文件不在，返回错误由调用方退出。
func Rollback(path string) error {
	m, err := LoadMarker(path)
	if err != nil {
		return err
	}
	if m == nil {
		return errors.New("无升级标记，无法自动回滚")
	}
	if m.Attempts >= 1 {
		return errors.New("已回滚过一次仍启动失败，停止重试（请检查 .old 旧版或手动恢复）")
	}
	if st, err := os.Stat(m.OldBin); err != nil || st.IsDir() {
		return fmt.Errorf("旧版 %s 不存在，无法自动回滚", m.OldBin)
	}
	m.Attempts++
	_ = writeMarker(path, m)
	logf("启动失败，自动回滚到旧版 %s", m.OldBin)
	return execFn(m.OldBin, os.Args)
}

// ── 分发束（server → agent 的多部传输格式，两端共用）────────────────────

// WriteBundle 把 manifest 原文/签名/更新包写进一个 multipart writer
// （调用方创建并负责响应头：完成后取 FormDataContentType()）。
func WriteBundle(mw *multipart.Writer, manifestRaw []byte, sig, pkg []byte) error {
	part, err := mw.CreateFormFile("manifest", manifestName)
	if err == nil {
		_, err = part.Write(manifestRaw)
	}
	if err == nil {
		part, err = mw.CreateFormFile("signature", manifestSig)
		if err == nil {
			_, err = part.Write([]byte(sig))
		}
	}
	if err == nil {
		part, err = mw.CreateFormFile("package", "lz-agent.gz")
		if err == nil {
			_, err = part.Write(pkg)
		}
	}
	if err != nil {
		return err
	}
	return mw.Close()
}

// NewBundleReader 按请求 Content-Type 建 multipart 读取器。
func NewBundleReader(contentType string, r io.Reader) (*multipart.Reader, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("Content-Type 解析失败: %w", err)
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, errors.New("缺少 multipart boundary")
	}
	return multipart.NewReader(r, boundary), nil
}

// ParseBundle 解析分发束，逐段限长防滥用。
func ParseBundle(mr *multipart.Reader, maxPackage int64) (manifestRaw []byte, sig string, pkg []byte, err error) {
	if maxPackage <= 0 || maxPackage > maxPackageSize {
		maxPackage = maxPackageSize
	}
	seen := map[string]bool{}
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			return nil, "", nil, perr
		}
		name := part.FormName()
		switch name {
		case "manifest":
			manifestRaw, err = io.ReadAll(io.LimitReader(part, 1<<20))
		case "signature":
			var b []byte
			b, err = io.ReadAll(io.LimitReader(part, 4096))
			sig = string(b)
		case "package":
			pkg, err = io.ReadAll(io.LimitReader(part, maxPackage+1))
			if err == nil && int64(len(pkg)) > maxPackage {
				err = errors.New("更新包超过大小上限")
			}
		}
		_ = part.Close()
		if err != nil {
			return nil, "", nil, err
		}
		seen[name] = true
	}
	if !seen["manifest"] || !seen["signature"] || !seen["package"] {
		return nil, "", nil, errors.New("分发束缺段（需 manifest/signature/package）")
	}
	if len(pkg) == 0 {
		return nil, "", nil, errors.New("更新包为空")
	}
	return manifestRaw, sig, pkg, nil
}
