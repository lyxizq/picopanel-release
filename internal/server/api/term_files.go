package api

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pkg/sftp"

	"lz-monitor/internal/server/term"
)

// fileEntry SFTP 目录列表条目。
type fileEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mode  string `json:"mode"` // 如 drwxr-xr-x / -rw-r--r--
	Perm  string `json:"perm"` // 权限八进制（如 755）
	Mtime int64  `json:"mtime"`
	IsDir bool   `json:"is_dir"`
	Owner string `json:"owner"` // 所有者用户名（远端 /etc/passwd 解析；失败回落数字 uid）
}

// fileDisk 磁盘使用概览（根分区，状态栏进度条用）。
type fileDisk struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}

// withSFTP 取池化 SFTP 连接执行操作（SFTPHold 持有整个 handler 生命周期，
// 大文件下载等长流式响应不会被空闲回收器中途掐断）；连接类错误丢弃连接
// 重试一次（长 idle 后连接被中间设备掐断属正常路径）。
func (h *TermHandler) withSFTP(c *gin.Context, id int64, fn func(cl *sftp.Client) error) {
	cl, release, err := h.term.SFTPHold(id)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer release()
	if err := fn(cl); err != nil {
		if isSFTPConnErr(err) {
			h.term.DropSFTP(id)
			if cl2, rel2, e2 := h.term.SFTPHold(id); e2 == nil {
				err = fn(cl2)
				rel2()
			}
		}
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "SFTP 操作失败: " + err.Error()})
		}
	}
}

// termID 校验 server_id 查询参数。
func termID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Query("server_id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的服务器 ID"})
		return 0, false
	}
	return id, true
}

// termPath 校验 path 查询参数：必须绝对路径，Clean 归一。
func termPath(c *gin.Context) (string, bool) {
	p := strings.TrimSpace(c.Query("path"))
	if p == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path 必填"})
		return "", false
	}
	if !strings.HasPrefix(p, "/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path 必须是绝对路径"})
		return "", false
	}
	return path.Clean(p), true
}

// ListFiles 列目录：目录在前、名称不区分大小写排序。
func (h *TermHandler) ListFiles(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	dir, ok := termPath(c)
	if !ok {
		return
	}
	h.withSFTP(c, id, func(cl *sftp.Client) error {
		infos, err := cl.ReadDir(dir)
		if err != nil {
			return err
		}
		sort.Slice(infos, func(i, j int) bool {
			if infos[i].IsDir() != infos[j].IsDir() {
				return infos[i].IsDir()
			}
			return strings.ToLower(infos[i].Name()) < strings.ToLower(infos[j].Name())
		})
		// 所有者列：远端 /etc/passwd 解析 uid→用户名（带缓存）；解析失败
		//（nil）或未命中时回退显示数字 uid
		names := h.term.UserNames(id)
		out := make([]fileEntry, 0, len(infos))
		for _, fi := range infos {
			name := fi.Name()
			if name == "" {
				continue
			}
			e := fileEntry{
				Name:  name,
				Size:  fi.Size(),
				Mode:  fi.Mode().String(),
				Perm:  fmt.Sprintf("%o", fi.Mode().Perm()),
				Mtime: fi.ModTime().Unix(),
				IsDir: fi.IsDir(),
			}
			if st, ok := fi.Sys().(*sftp.FileStat); ok {
				if n := names[st.UID]; n != "" {
					e.Owner = n
				} else {
					e.Owner = strconv.FormatUint(uint64(st.UID), 10)
				}
			}
			out = append(out, e)
		}
		// 底部状态栏磁盘条：只看根分区（/）；查询失败缺省，不影响列表。
		// total=总块×块大小；used=总块-空闲块（Bfree 对 root 计可释放缓存，
		// 与 df 的 used 口径一致）
		var disk *fileDisk
		if vfs, err := cl.StatVFS("/"); err == nil && vfs.Blocks > 0 && vfs.Bsize > 0 {
			disk = &fileDisk{
				Total: int64(vfs.Blocks * vfs.Bsize),
				Used:  int64((vfs.Blocks - vfs.Bfree) * vfs.Bsize),
			}
		}
		c.JSON(http.StatusOK, gin.H{"path": dir, "files": out, "disk": disk})
		return nil
	})
}

// Download 下载文件（目录返回 400）。Content-Disposition 文件名做
// RFC 5987 编码，中文文件名各浏览器可正确落地。写超时按请求取消
// （全局 30s WriteTimeout 会掐断大文件的慢速流式写）。
// *sftp.File 实现 io.ReadSeeker，交给 http.ServeContent 免费获得
// Range/206 分段响应——浏览器端断点续传的基础。
func (h *TermHandler) Download(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	p, ok := termPath(c)
	if !ok {
		return
	}
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})
	h.withSFTP(c, id, func(cl *sftp.Client) error {
		f, err := cl.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if st.IsDir() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "目录不能下载，请下载具体文件"})
			return nil
		}
		name := path.Base(p)
		c.Header("Content-Disposition",
			`attachment; filename="`+sanitizeASCII(name)+`"; filename*=UTF-8''`+urlQueryEscape(name))
		http.ServeContent(c.Writer, c.Request, name, st.ModTime(), f)
		return nil
	})
}

// uploadPartExt 远端上传临时文件后缀：先写 <name>.lzpart，传完原子改名
// 为正式名——目标目录中途看到的永远是完整正式文件（或一个明显的临时残片，
// 取消/断线保留它供断点续传，前端「清除记录」时连带清理）。
const uploadPartExt = ".lzpart"

// maxUploadBytes 单文件上传上限（5G）：最后一个分片写完后校验临时文件大小，
// 超限删残片报错，保护远端磁盘不被垃圾数据占满。
const maxUploadBytes = 5 << 30

// Upload multipart 流式上传（MultipartReader 逐 part 拷贝，不落内存盘）。
// 每个带 filename 的 part 写为 path 目录下的 <name>.lzpart，写完改名正式名；
// 无 filename 的字段（前端附带的说明）跳过。文件名做 Base 清洗，防 ../ 逃逸。
// 断点续传：请求头 X-Upload-Offset=N（字节数）时从远端临时文件第 N 字节起
// 追加（OpenFile 不截断 + Seek），要求临时文件现有大小恰为 N（不对齐直接
// 报错，前端重新查询 size 再试），仅支持单文件续传。读超时按请求取消
// （全局 30s ReadTimeout 会掐断慢客户端的大文件上传）。
// 分片模式：X-Upload-Final=0 表示后面还有分片——只写 .lzpart 不改名
// （正式文件必须等全部数据到齐才出现），最后一片带 X-Upload-Final=1 触发改名；
// 头缺省=整文件单请求（兼容旧前端/小文件），写完即改名。单文件超 5G 删残片报错。
func (h *TermHandler) Upload(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	dir, ok := termPath(c)
	if !ok {
		return
	}
	offset := int64(0)
	if v := c.GetHeader("X-Upload-Offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "X-Upload-Offset 应为非负整数"})
			return
		}
		offset = n
	}
	doRename := true
	if v := c.GetHeader("X-Upload-Final"); v != "" {
		if v != "0" && v != "1" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "X-Upload-Final 应为 0 或 1"})
			return
		}
		doRename = v == "1"
	}
	rc := http.NewResponseController(c.Writer)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	mr, err := c.Request.MultipartReader()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "multipart 请求无效"})
		return
	}
	var uploaded []string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "读取上传数据失败"})
			return
		}
		name := path.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
		if name == "" || name == "." || name == "/" {
			continue // 非文件字段或清洗后为空
		}
		if offset > 0 && len(uploaded) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "断点续传仅支持单个文件"})
			return
		}
		target := path.Join(dir, name)
		tmp := target + uploadPartExt
		done := false
		h.withSFTP(c, id, func(cl *sftp.Client) error {
			var f *sftp.File
			if offset > 0 {
				// 续传：不截断打开，校验临时文件现有大小与偏移一致后定位追加
				st, err := cl.Stat(tmp)
				if err != nil {
					return fmt.Errorf("查询远端临时文件失败: %w", err)
				}
				if st.Size() != offset {
					return fmt.Errorf("远端临时文件大小 %d 与续传偏移 %d 不一致，请重新续传", st.Size(), offset)
				}
				f, err = cl.OpenFile(tmp, os.O_WRONLY|os.O_CREATE)
				if err != nil {
					return err
				}
				if _, err := f.Seek(offset, io.SeekStart); err != nil {
					f.Close()
					return err
				}
			} else {
				var err error
				f, err = cl.Create(tmp) // 全新写（截断覆盖，连同上次残片）
				if err != nil {
					return err
				}
			}
			if _, err := io.Copy(f, part); err != nil {
				f.Close() // 取消/断线：保留 .lzpart 供续传，前端清除记录时清理
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			// 单文件 5G 上限：超限删残片（续传无意义）
			if st, err := cl.Stat(tmp); err == nil && st.Size() > maxUploadBytes {
				_ = cl.Remove(tmp)
				return fmt.Errorf("文件超过单文件 5G 上传上限，已中止并清理临时文件")
			}
			if doRename {
				// 写完原子改名（同目录 rename 是元数据操作）；目标同名将被覆盖
				if err := cl.PosixRename(tmp, target); err != nil {
					if err2 := cl.Rename(tmp, target); err2 != nil {
						return err2
					}
				}
			}
			uploaded = append(uploaded, name)
			done = true
			return nil
		})
		if !done {
			return // withSFTP 已写错误响应
		}
	}
	c.JSON(http.StatusOK, gin.H{"uploaded": uploaded})
}

// SFTPAuth 文件页「连接」：提交本次使用的 SSH 凭据试连并替换该服务器的
// 池化 SFTP 连接。凭据不落日志；save=true 时连接成功后密码入库（用户名
// 总是入库），否则只存服务端进程内存（重启后需重输）。响应不回显凭据。
func (h *TermHandler) SFTPAuth(c *gin.Context) {
	var req sftpAuthReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（server_id 必填，用户名/密码过长）"})
		return
	}
	if err := h.term.SFTPAuth(req.ServerID, term.Cred{Username: req.Username, Password: req.Password}, req.Save); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

type sftpAuthReq struct {
	ServerID int64  `json:"server_id" binding:"required"`
	Username string `json:"username" binding:"omitempty,max=100"`
	Password string `json:"password" binding:"omitempty,max=200"`
	Save     bool   `json:"save"`
}

// maxEditBytes 在线编辑的文件大小上限：文本框加载更大内容既卡浏览器，
// 也长时间占用 SFTP 连接。
const maxEditBytes = 5 << 20

type fileOpReq struct {
	Op        string   `json:"op" binding:"required,oneof=mkdir rename remove touch count size read write chmod chown copy move users"`
	Path      string   `json:"path"`      // 操作目标（copy/move 时为目的地，其余为对象）
	To        string   `json:"to"`        // rename 目标（绝对路径）
	From      string   `json:"from"`      // copy/move 源（绝对路径）
	Content   string   `json:"content"`   // write 文件内容
	Perm      string   `json:"perm"`      // chmod 八进制权限（如 755）
	Owner     string   `json:"owner"`     // chown 目标用户名
	Recursive bool     `json:"recursive"` // chmod/chown 是否应用到子目录
	Paths     []string `json:"paths"`     // 打包成员（绝对路径）
	Name      string   `json:"name"`      // 压缩包基础名（不含扩展名）
	Force     bool     `json:"force"`     // copy/move 目标已存在时覆盖
}

// FileOp 文件操作：mkdir（含父目录）/ rename（目标同名将被覆盖，类 mv）/
// remove（递归删除，含非空目录）/ touch（新建空文件，已存在报错）/
// count（递归统计内容量，删除前确认框与属性页用）/ size（查远端文件现有大小，
// 上传断点续传前对齐偏移与粘贴前探测存在性用）/ read/write（在线编辑）/
// chmod/chown（改权限/所有者，可选递归，符号链接不跟随）/
// copy/move（递归复制/移动，跨文件系统自动回落复制+删除）/
// times（属性页访问时间，SFTP 原生 atime）/
// users（远端用户名列表，改所有者下拉用）。
// 创建压缩包走 /term/files/task（后台任务，带进度轮询）。
func (h *TermHandler) FileOp(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	var req fileOpReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（op 必填，取值见文档）"})
		return
	}
	// 按操作类型校验路径参数（users 不需要单个 path）
	p := ""
	if req.Op != "users" {
		p = strings.TrimSpace(req.Path)
		if p == "" || !strings.HasPrefix(p, "/") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "path 必须是绝对路径"})
			return
		}
		p = path.Clean(p)
	}
	to := ""
	if req.Op == "rename" {
		to = strings.TrimSpace(req.To)
		if to == "" || !strings.HasPrefix(to, "/") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "to 必须是绝对路径"})
			return
		}
		to = path.Clean(to)
	}
	from := ""
	if req.Op == "copy" || req.Op == "move" {
		from = strings.TrimSpace(req.From)
		if from == "" || !strings.HasPrefix(from, "/") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "from 必须是绝对路径"})
			return
		}
		from = path.Clean(from)
	}
	h.withSFTP(c, id, func(cl *sftp.Client) error {
		switch req.Op {
		case "mkdir":
			if err := cl.MkdirAll(p); err != nil {
				return err
			}
		case "rename":
			// PosixRename 覆盖语义（类 mv）；服务器不支持扩展时回落标准 Rename
			if err := cl.PosixRename(p, to); err != nil {
				if err2 := cl.Rename(p, to); err2 != nil {
					return err2
				}
			}
		case "remove":
			// 递归删除（文件/空目录/非空目录皆可）；符号链接只删链接本身不跟随
			if err := cl.RemoveAll(p); err != nil {
				return err
			}
		case "touch":
			if _, err := cl.Stat(p); err == nil {
				return fmt.Errorf("已存在同名文件/目录")
			}
			f, err := cl.Create(p)
			if err != nil {
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case "count":
			files, dirs, size, err := sftpCount(cl, p)
			if err != nil {
				return err
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "files": files, "dirs": dirs, "size": size})
			return nil
		case "size":
			st, err := cl.Stat(p)
			if err != nil {
				return err
			}
			if st.IsDir() {
				return fmt.Errorf("目标是目录")
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "size": st.Size()})
			return nil
		case "times":
			// 访问时间（SFTP v3 原生 ACMODTIME 属性；创建时间协议不支持，不取）
			st, err := cl.Stat(p)
			if err != nil {
				return err
			}
			atime := int64(0)
			if fs, ok := st.Sys().(*sftp.FileStat); ok {
				atime = int64(fs.Atime)
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "atime": atime})
			return nil
		case "read":
			st, err := cl.Stat(p)
			if err != nil {
				return err
			}
			if st.IsDir() {
				return fmt.Errorf("目标是目录")
			}
			if st.Size() > maxEditBytes {
				return fmt.Errorf("文件超过 5MB，不支持在线编辑")
			}
			f, err := cl.Open(p)
			if err != nil {
				return err
			}
			data, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				return err
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "content": string(data), "size": st.Size()})
			return nil
		case "write":
			if len(req.Content) > maxEditBytes {
				return fmt.Errorf("内容超过 5MB，不支持在线保存")
			}
			f, err := cl.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
			if err != nil {
				return err
			}
			if _, err := f.Write([]byte(req.Content)); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case "chmod":
			v, err := strconv.ParseUint(strings.TrimSpace(req.Perm), 8, 32)
			if err != nil || v > 0o777 {
				return fmt.Errorf("权限数字无效（应为 000~777 八进制）")
			}
			mode := os.FileMode(uint32(v)).Perm()
			if err := cl.Chmod(p, mode); err != nil {
				return err
			}
			if req.Recursive {
				if err := sftpWalk(cl, p, func(sp string, fi os.FileInfo) error {
					if fi.Mode()&os.ModeSymlink != 0 { // 符号链接不跟随，防环
						return nil
					}
					return cl.Chmod(sp, mode)
				}); err != nil {
					return err
				}
			}
		case "chown":
			ids, err := h.term.UserIDs(id)
			if err != nil {
				return err
			}
			pair, ok := ids[strings.TrimSpace(req.Owner)]
			if !ok {
				return fmt.Errorf("远端不存在用户 %s", req.Owner)
			}
			uid, gid := int(pair[0]), int(pair[1])
			if err := cl.Chown(p, uid, gid); err != nil {
				return err
			}
			if req.Recursive {
				if err := sftpWalk(cl, p, func(sp string, fi os.FileInfo) error {
					if fi.Mode()&os.ModeSymlink != 0 {
						return nil
					}
					return cl.Chown(sp, uid, gid)
				}); err != nil {
					return err
				}
			}
		case "copy", "move":
			// 目的地已存在：未带 force 报错（前端先探测再确认覆盖），带 force 先删后写
			if _, err := cl.Lstat(p); err == nil {
				if !req.Force {
					return fmt.Errorf("目标已存在：%s", path.Base(p))
				}
				if err := cl.RemoveAll(p); err != nil {
					return err
				}
			}
			if req.Op == "move" {
				// 优先原地改名（元数据操作）；跨文件系统改名失败回落复制+删除
				if err := cl.PosixRename(from, p); err == nil {
					break
				}
				ctr := 0
				if err := sftpCopy(cl, from, p, &ctr); err != nil {
					return err
				}
				if err := cl.RemoveAll(from); err != nil {
					return fmt.Errorf("复制成功但删除源失败: %w", err)
				}
			} else {
				ctr := 0
				if err := sftpCopy(cl, from, p, &ctr); err != nil {
					return err
				}
			}
		case "users":
			ids, err := h.term.UserIDs(id)
			if err != nil {
				return err
			}
			names := make([]string, 0, len(ids))
			for n := range ids {
				names = append(names, n)
			}
			sort.Strings(names)
			c.JSON(http.StatusOK, gin.H{"code": 0, "users": names})
			return nil
		}
		c.JSON(http.StatusOK, gin.H{"code": 0})
		return nil
	})
}

// sftpWalk 递归遍历目录树（符号链接不跟随，防环），fn 对每个条目调用
// （目录自身不传入，由调用方单独处理）。
func sftpWalk(cl *sftp.Client, dir string, fn func(p string, fi os.FileInfo) error) error {
	infos, err := cl.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, fi := range infos {
		p := path.Join(dir, fi.Name())
		if err := fn(p, fi); err != nil {
			return err
		}
		if fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 {
			if err := sftpWalk(cl, p, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// sftpCopy 递归复制：目录建目录后逐项复制；符号链接重建链接（不复制内容）；
// 文件按源权限流式拷贝。ctr 计条目数，超 sftpCountMax 报错防呆。
func sftpCopy(cl *sftp.Client, from, to string, ctr *int) error {
	fi, err := cl.Lstat(from)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := cl.ReadLink(from)
		if err != nil {
			return err
		}
		return cl.Symlink(target, to)
	}
	if fi.IsDir() {
		*ctr++
		if *ctr > sftpCountMax {
			return fmt.Errorf("目录条目超过 %d，请分批操作", sftpCountMax)
		}
		if err := cl.Mkdir(to); err != nil {
			return err
		}
		if err := cl.Chmod(to, fi.Mode().Perm()); err != nil {
			return err
		}
		infos, err := cl.ReadDir(from)
		if err != nil {
			return err
		}
		for _, ci := range infos {
			if err := sftpCopy(cl, path.Join(from, ci.Name()), path.Join(to, ci.Name()), ctr); err != nil {
				return err
			}
		}
		return nil
	}
	*ctr++
	if *ctr > sftpCountMax {
		return fmt.Errorf("目录条目超过 %d，请分批操作", sftpCountMax)
	}
	src, err := cl.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := cl.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	defer dst.Close()
	if err := dst.Chmod(fi.Mode().Perm()); err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	return err
}

// sftpArchive 把同一目录下的若干条目打为 tar.gz 存回该目录，返回最终文件名。
// 同名自动加 (1)(2)... 序号；任一步失败删除半成品后报错。prog 非空时随写随报
// 已写字节数（tar 头开销忽略不计，供后台任务进度条用）。stop 非空时随写随查
// 取消标志（后台任务取消用，返回 errTaskCanceled；半成品同样被清理）。
func sftpArchive(cl *sftp.Client, paths []string, base string, prog func(done int64), stop func() bool) (string, error) {
	dir := path.Dir(paths[0])
	for _, p := range paths {
		if path.Dir(p) != dir {
			return "", fmt.Errorf("打包项必须在同一目录下")
		}
	}
	if base == "" {
		base = "归档" + time.Now().Format("20060102150405")
	}
	final := base + ".tar.gz"
	for i := 1; i <= 99; i++ {
		if _, err := cl.Stat(path.Join(dir, final)); err != nil {
			break
		}
		final = fmt.Sprintf("%s(%d).tar.gz", base, i)
	}
	remote := path.Join(dir, final)
	f, err := cl.OpenFile(remote, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return "", err
	}
	gw := gzip.NewWriter(f)
	var tw *tar.Writer
	if prog != nil || stop != nil {
		tw = tar.NewWriter(&progWriter{w: gw, fn: prog, stop: stop})
	} else {
		tw = tar.NewWriter(gw)
	}
	ctr := 0
	var fail error
	for _, p := range paths {
		if stop != nil && stop() { // 打包成员之间响应取消
			fail = errTaskCanceled
			break
		}
		if err := archiveAdd(cl, tw, p, path.Base(p), &ctr); err != nil {
			fail = err
			break
		}
	}
	if fail == nil {
		fail = tw.Close()
	} else {
		tw.Close()
	}
	if fail == nil {
		fail = gw.Close()
	} else {
		gw.Close()
	}
	if fail == nil {
		fail = f.Close()
	} else {
		f.Close()
	}
	if fail != nil {
		_ = cl.RemoveAll(remote)
		return "", fail
	}
	return final, nil
}

// archiveAdd 向 tar.Writer 写入一个条目（目录递归其内容）。包内路径 rel 相对
// 打包根，符号链接按链接写入。条目数超 sftpCountMax 报错防呆。
func archiveAdd(cl *sftp.Client, tw *tar.Writer, ap, rel string, ctr *int) error {
	fi, err := cl.Lstat(ap)
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:    rel,
		ModTime: fi.ModTime(),
		Mode:    int64(fi.Mode().Perm()),
		Format:  tar.FormatPAX, // 中文文件名需要
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		target, err := cl.ReadLink(ap)
		if err != nil {
			return err
		}
		hdr.Typeflag = tar.TypeSymlink
		hdr.Linkname = target
		return tw.WriteHeader(hdr)
	case fi.IsDir():
		*ctr++
		if *ctr > sftpCountMax {
			return fmt.Errorf("目录条目超过 %d，请分批打包", sftpCountMax)
		}
		hdr.Typeflag = tar.TypeDir
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		infos, err := cl.ReadDir(ap)
		if err != nil {
			return err
		}
		for _, ci := range infos {
			if err := archiveAdd(cl, tw, path.Join(ap, ci.Name()), rel+"/"+ci.Name(), ctr); err != nil {
				return err
			}
		}
		return nil
	default:
		*ctr++
		if *ctr > sftpCountMax {
			return fmt.Errorf("目录条目超过 %d，请分批打包", sftpCountMax)
		}
		hdr.Typeflag = tar.TypeReg
		hdr.Size = fi.Size()
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := cl.Open(ap)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.CopyN(tw, f, fi.Size())
		return err
	}
}

// ArchiveDownload GET /term/files/archive?server_id=&paths=JSON数组：
// 多选/目录即时打为 tar.gz 流式下载（远端不落临时文件）。路径预检放在写
// 响应头之前，流一旦开始出错只中止（无法再改状态码）。
func (h *TermHandler) ArchiveDownload(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	var paths []string
	if raw := c.Query("paths"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &paths); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "paths 参数无效"})
			return
		}
	}
	if len(paths) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未选择要打包的项目"})
		return
	}
	for i := range paths {
		pp := strings.TrimSpace(paths[i])
		if pp == "" || !strings.HasPrefix(pp, "/") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "打包路径必须是绝对路径"})
			return
		}
		paths[i] = path.Clean(pp)
	}
	cl, release, err := h.term.SFTPHold(id)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer release()           // 流式打包期间持有连接，空闲回收器不回收
	for _, p := range paths { // 预检：连接失活/路径不存在在写头之前暴露
		if _, err := cl.Lstat(p); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "SFTP 操作失败: " + err.Error()})
			return
		}
	}
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})
	fname := "files-" + time.Now().Format("20060102150405") + ".tar.gz"
	c.Header("Content-Type", "application/gzip")
	c.Header("Content-Disposition",
		`attachment; filename="`+fname+`"; filename*=UTF-8''`+urlQueryEscape(fname))
	gw := gzip.NewWriter(c.Writer)
	tw := tar.NewWriter(gw)
	ctr := 0
	for _, p := range paths {
		if err := archiveAdd(cl, tw, p, path.Base(p), &ctr); err != nil {
			log.Printf("打包下载中止 %s: %v", p, err)
			return
		}
	}
	tw.Close()
	gw.Close()
}

// sftpCountMax 统计/防呆上限：条目超过 20 万才报错。上限只为拦病理性规模
// （百万级条目的失控遍历），不应挡住真实大目录的正当操作——node_modules
// 级目录轻松超 2 万，故从 2 万放宽到 20 万；超大目录操作耗时长属用户自担。
const sftpCountMax = 200000

// sftpCount 递归统计一个路径的内容量（文件数/子目录数/总大小，不含路径本身）。
func sftpCount(cl *sftp.Client, p string) (files, dirs int, size int64, err error) {
	st, err := cl.Stat(p)
	if err != nil {
		return 0, 0, 0, err
	}
	if !st.IsDir() {
		return 1, 0, st.Size(), nil
	}
	var walk func(dir string) error
	walk = func(dir string) error {
		infos, e := cl.ReadDir(dir)
		if e != nil {
			return e
		}
		for _, fi := range infos {
			if fi.IsDir() {
				dirs++
				if files+dirs > sftpCountMax {
					return fmt.Errorf("目录条目超过 %d，请分批删除", sftpCountMax)
				}
				if e := walk(path.Join(dir, fi.Name())); e != nil {
					return e
				}
			} else {
				files++
				size += fi.Size()
			}
		}
		return nil
	}
	if e := walk(p); e != nil {
		return 0, 0, 0, e
	}
	return files, dirs, size, nil
}

// isSFTPConnErr 判断是否连接级错误（值得丢弃连接重试一次）。
// pkg/sftp 连接断开表现为 io.ErrUnexpectedEOF / 网络错误；SSH 层断开
// 会包装进 *ssh.OpenChannelError 或直接 EOF。
func isSFTPConnErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "timeout")
}

// sanitizeASCII 剥离非 ASCII 字符做 filename 回退值。
func sanitizeASCII(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f && r != '"' && r != '\\' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// urlQueryEscape path segment 转义（RFC 5987 filename*）。
func urlQueryEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '_' || ch == '.' || ch == '~' {
			b.WriteByte(ch)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[ch>>4])
		b.WriteByte(hex[ch&0xf])
	}
	return b.String()
}
