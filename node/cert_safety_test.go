package node

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCertificateKeyAndHostname(t *testing.T) {
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "key.pem")
	if err := generateSelfSslCertificate("localhost", cert, key); err != nil {
		t.Fatal(err)
	}
	if err := validateCertificate(cert, key, "localhost"); err != nil {
		t.Fatal(err)
	}
	if err := validateCertificate(cert, key, "wrong.invalid"); err == nil {
		t.Fatal("accepted wrong hostname")
	}
	info, err := os.Stat(key)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("key is not private")
	}
	if _, err := (&User{}).DecodePrivate("corrupt PEM"); err == nil {
		t.Fatal("accepted bad account key")
	}
	if _, err := (&Lego{}).CheckCert([]byte("corrupt PEM")); err == nil {
		t.Fatal("accepted bad certificate")
	}
}
