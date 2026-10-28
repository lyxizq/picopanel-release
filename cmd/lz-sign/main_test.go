package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 走一遍真实链路：生成密钥对写文件 → 按文件加载 → 签名 → 验签通过；
// 再验内容篡改、签名篡改、换钥验签三种必须失败的情形。
func TestGenkeySignVerifyRoundtrip(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "release.key")
	pubPath := filepath.Join(dir, "picopanel.pub")

	if err := cmdGenkey([]string{"-key", keyPath, "-pub", pubPath}); err != nil {
		t.Fatalf("genkey: %v", err)
	}
	// 已存在时未加 -f 必须拒绝（防误覆盖废掉已发版本）
	if err := cmdGenkey([]string{"-key", keyPath, "-pub", pubPath}); err == nil {
		t.Fatal("genkey 重复执行应拒绝覆盖")
	}

	msgPath := filepath.Join(dir, "manifest.json")
	msg := []byte(`{"schema":1,"version":"v1.0.1"}`)
	if err := os.WriteFile(msgPath, msg, 0o644); err != nil {
		t.Fatal(err)
	}
	sigPath := msgPath + ".sig"
	if err := cmdSign([]string{"-key", keyPath, "-i", msgPath, "-s", sigPath}); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := cmdVerify([]string{"-pub", pubPath, "-i", msgPath, "-s", sigPath}); err != nil {
		t.Fatalf("verify 原文应通过: %v", err)
	}

	// 内容篡改必须失败
	tampered := append([]byte{}, msg...)
	tampered[len(tampered)-2] = '2'
	badPath := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badPath, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdVerify([]string{"-pub", pubPath, "-i", badPath, "-s", sigPath}); err == nil {
		t.Fatal("内容被篡改的验签应失败")
	}

	// 签名篡改必须失败
	raw, err := os.ReadFile(sigPath)
	if err != nil {
		t.Fatal(err)
	}
	forged := append([]byte{}, raw...)
	if forged[1] == 'a' {
		forged[1] = 'b'
	} else {
		forged[1] = 'a'
	}
	forgedPath := filepath.Join(dir, "forged.sig")
	if err := os.WriteFile(forgedPath, forged, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdVerify([]string{"-pub", pubPath, "-i", msgPath, "-s", forgedPath}); err == nil {
		t.Fatal("签名被篡改的验签应失败")
	}

	// 换一把公钥必须失败
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(dir, "other.pub")
	if err := os.WriteFile(otherPath, []byte(hex.EncodeToString(otherPub)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdVerify([]string{"-pub", otherPath, "-i", msgPath, "-s", sigPath}); err == nil {
		t.Fatal("不匹配公钥的验签应失败")
	}

	// pubkey 推导的公钥应与 genkey 落盘的一致
	pubFile, err := os.ReadFile(pubPath)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := pubHexFromKeyFile(keyPath)
	if err != nil {
		t.Fatalf("pubkey 推导: %v", err)
	}
	if derived != strings.TrimSpace(string(pubFile)) {
		t.Fatalf("推导公钥 %s 与落盘公钥 %s 不一致", derived, strings.TrimSpace(string(pubFile)))
	}
}
