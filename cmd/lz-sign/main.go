// lz-sign 是发布通道（TODO 404，GitHub Releases 单源）的 ed25519 签名小工具：
// 生成密钥对、对更新 manifest 签名、验签。私钥只存发布机仓库外
// （缺省 ~/.picopanel/release.key，泄露=所有用户可被投毒、丢失=无法再签版）；
// 公钥 deploy/picopanel.pub 提交进仓库，构建时注入两端二进制供在线更新验签
// （客户端验签在 TODO 395/396 接线）。密钥与签名文件一律存 hex 文本。
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	keyPerm = 0o600 // 私钥文件权限
	pubPerm = 0o644
)

func defaultKeyPath() string {
	return filepath.Join(os.Getenv("HOME"), ".picopanel", "release.key")
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "genkey":
		err = cmdGenkey(os.Args[2:])
	case "sign":
		err = cmdSign(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	case "pubkey":
		err = cmdPubkey(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `lz-sign — 发布通道 ed25519 签名工具（TODO 404）

用法:
  lz-sign genkey [-key 私钥路径] [-pub 公钥路径] [-f]
      生成密钥对；缺省私钥 %s、公钥 deploy/picopanel.pub。
      已存在时拒绝覆盖（确认重来加 -f）。
  lz-sign sign -i 文件 [-key 私钥路径] [-s 签名输出]
      对文件签名；缺省输出 <文件>.sig，-s - 输出到 stdout。
  lz-sign verify -i 文件 -s 签名 [-pub 公钥路径]
      验签，通过退出码 0，否则非 0。
  lz-sign pubkey [-key 私钥路径]
      从私钥推导打印公钥 hex。
`, defaultKeyPath())
	os.Exit(2)
}

func cmdGenkey(args []string) error {
	fs := flag.NewFlagSet("genkey", flag.ExitOnError)
	keyPath := fs.String("key", defaultKeyPath(), "私钥输出路径")
	pubPath := fs.String("pub", "deploy/picopanel.pub", "公钥输出路径")
	force := fs.Bool("f", false, "覆盖已存在的密钥文件")
	fs.Parse(args)
	if _, err := os.Stat(*keyPath); err == nil && !*force {
		return fmt.Errorf("私钥已存在: %s（覆盖会让已发版本无法验签；确认重来加 -f）", *keyPath)
	}
	if _, err := os.Stat(*pubPath); err == nil && !*force {
		return fmt.Errorf("公钥已存在: %s（确认重来加 -f）", *pubPath)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*keyPath), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*pubPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*keyPath, []byte(hex.EncodeToString(priv)+"\n"), keyPerm); err != nil {
		return err
	}
	if err := os.WriteFile(*pubPath, []byte(hex.EncodeToString(pub)+"\n"), pubPerm); err != nil {
		return err
	}
	fmt.Printf("已生成密钥对\n  私钥: %s（仓库外保管：泄露=所有用户可被投毒，丢失=无法再签版）\n  公钥: %s（提交进仓库，随构建注入两端）\n", *keyPath, *pubPath)
	return nil
}

func cmdSign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	keyPath := fs.String("key", defaultKeyPath(), "私钥路径")
	in := fs.String("i", "", "待签名文件（必填）")
	out := fs.String("s", "", "签名输出路径（缺省 <输入>.sig；- 为 stdout）")
	fs.Parse(args)
	if *in == "" {
		return errors.New("缺 -i 待签名文件")
	}
	priv, err := loadPrivateKey(*keyPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	sig := hex.EncodeToString(ed25519.Sign(priv, data))
	if *out == "-" {
		fmt.Println(sig)
		return nil
	}
	if *out == "" {
		*out = *in + ".sig"
	}
	if err := os.WriteFile(*out, []byte(sig+"\n"), pubPerm); err != nil {
		return err
	}
	fmt.Printf("已签名: %s\n", *out)
	return nil
}

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pubPath := fs.String("pub", "deploy/picopanel.pub", "公钥路径")
	in := fs.String("i", "", "被签名的文件（必填）")
	sigPath := fs.String("s", "", "签名文件（必填，缺省取 <输入>.sig）")
	fs.Parse(args)
	if *in == "" {
		return errors.New("缺 -i 被签名文件")
	}
	if *sigPath == "" {
		*sigPath = *in + ".sig"
	}
	pub, err := loadPublicKey(*pubPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	sig, err := loadHexFile(*sigPath, ed25519.SignatureSize, "签名")
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, data, sig) {
		return errors.New("签名无效——文件或签名与公钥不匹配")
	}
	fmt.Println("签名有效")
	return nil
}

func cmdPubkey(args []string) error {
	fs := flag.NewFlagSet("pubkey", flag.ExitOnError)
	keyPath := fs.String("key", defaultKeyPath(), "私钥路径")
	fs.Parse(args)
	pubHex, err := pubHexFromKeyFile(*keyPath)
	if err != nil {
		return err
	}
	fmt.Println(pubHex)
	return nil
}

// pubHexFromKeyFile 从私钥文件推导公钥 hex（pubkey 子命令与测试共用）。
func pubHexFromKeyFile(keyPath string) (string, error) {
	priv, err := loadPrivateKey(keyPath)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(priv.Public().(ed25519.PublicKey)), nil
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := loadHexFile(path, ed25519.PrivateKeySize, "私钥")
	if err != nil {
		return nil, err
	}
	return ed25519.PrivateKey(raw), nil
}

func loadPublicKey(path string) (ed25519.PublicKey, error) {
	raw, err := loadHexFile(path, ed25519.PublicKeySize, "公钥")
	if err != nil {
		return nil, err
	}
	return ed25519.PublicKey(raw), nil
}

// loadHexFile 读 hex 文本密钥/签名文件并校验长度（期望字节数 want）。
func loadHexFile(path string, want int, what string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("%s 不是合法 hex: %s", what, path)
	}
	if len(raw) != want {
		return nil, fmt.Errorf("%s 长度 %d 字节，应为 %d: %s", what, len(raw), want, path)
	}
	return raw, nil
}
