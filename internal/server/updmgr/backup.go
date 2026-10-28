package updmgr

import (
	"fmt"
	"io"
	"os"
)

// RotateBackup 在线升级前的数据库文件级备份（395⑧「数据不丢失」的最硬一环）：
// 假定调用方已先 st.Checkpoint()（WAL 合并回主库），此后把主库文件轮换拷贝
// dst.1 ← 最新，旧的依次后移，至多保留 keep 份——store 迁移一旦有 bug，
// 用 .pre-upgrade.1 整库回退即可，数据一个字节不丢。
func RotateBackup(src string, keep int) error {
	if keep < 1 {
		keep = 1
	}
	// 依次后移：.keep-1 删除，.i ← .(i-1)
	oldest := fmt.Sprintf("%s.pre-upgrade.%d", src, keep)
	_ = os.Remove(oldest)
	for i := keep - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.pre-upgrade.%d", src, i)
		to := fmt.Sprintf("%s.pre-upgrade.%d", src, i+1)
		if _, err := os.Stat(from); err == nil {
			if err := os.Rename(from, to); err != nil {
				return err
			}
		}
	}
	return copyFile(src, fmt.Sprintf("%s.pre-upgrade.1", src))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// WAL/SHM 顺手拷走：checkpoint(TRUNCATE) 后它们应为空/极小，拷上防万一
	for _, ext := range []string{"-wal", "-shm"} {
		if b, err := os.ReadFile(src + ext); err == nil && len(b) > 0 {
			_ = os.WriteFile(dst+ext, b, 0o644)
		} else {
			_ = os.Remove(dst + ext)
		}
	}
	return os.Rename(tmp, dst)
}
