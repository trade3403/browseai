package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGitHubProtectedKey(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	raw := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	path := filepath.Join(t.TempDir(), "secret.pem")
	write := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(raw)
	if runtime.GOOS == "windows" {
		if _, err := loadGitHubAppKey(path); err == nil {
			t.Fatal("Windows ACL not verified")
		}
		return
	}
	for _, data := range [][]byte{raw, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})} {
		write(data)
		got, err := loadGitHubAppKey(path)
		if err != nil || got.N.Cmp(key.N) != 0 {
			t.Fatal("valid key rejected", err)
		}
	}
	for _, data := range [][]byte{
		[]byte("invalid"),
		[]byte("prefix" + string(raw)),
		[]byte(string(raw) + string(raw)),
		[]byte(strings.Repeat("x", 65537)),
	} {
		write(data)
		if _, err := loadGitHubAppKey(path); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
	write(raw)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadGitHubAppKey(path); err == nil {
		t.Fatal("public key file accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(path), "link.pem")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{link, "relative.pem", filepath.Dir(path), path + ".missing"} {
		if _, err := loadGitHubAppKey(bad); err == nil {
			t.Fatal("unsafe path accepted")
		}
	}
}
