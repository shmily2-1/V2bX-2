//go:build sing && xray && hysteria2 && with_quic

package core_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/core"
	_ "github.com/shmily2-1/V2bX-2/core/imports"
)

// These are real core/lifecycle tests. All listeners bind only to loopback
// and ephemeral ports; no panel, ACME, public DNS or production node is used.
func TestUpgradedCoreLifecycle(t *testing.T) {
	cert, key := localCertificate(t)
	configs := []conf.CoreConfig{
		{Type: "xray", XrayConfig: conf.NewXrayConfig()},
		{Type: "sing", SingConfig: conf.NewSingConfig()},
		{Type: "hysteria2", Hysteria2Config: conf.NewHysteria2Config()},
	}
	for _, cfg := range configs {
		t.Run(cfg.Type, func(t *testing.T) {
			engine, err := core.NewCore([]conf.CoreConfig{cfg})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := engine.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := engine.Start(); err != nil {
				t.Fatal(err)
			}
			for _, protocol := range engine.Protocols() {
				t.Run(protocol, func(t *testing.T) {
					tag := cfg.Type + "-" + protocol
					info := &panel.NodeInfo{
						Type: protocol, Security: panel.Tls,
						Common:      &panel.CommonNode{ServerPort: 0},
						VAllss:      &panel.VAllssNode{Network: "tcp"},
						Trojan:      &panel.TrojanNode{Network: "tcp"},
						Shadowsocks: &panel.ShadowsocksNode{Cipher: "aes-128-gcm"},
						Tuic:        &panel.TuicNode{CongestionControl: "cubic"},
						AnyTls:      &panel.AnyTlsNode{},
						Hysteria:    &panel.HysteriaNode{UpMbps: 100, DownMbps: 100},
						Hysteria2:   &panel.Hysteria2Node{},
					}
					if cfg.Type == "xray" {
						// Xray uses port 0 to select Unix sockets, not ephemeral TCP.
						listener, err := net.Listen("tcp", "127.0.0.1:0")
						if err != nil {
							t.Fatal(err)
						}
						info.Common.ServerPort = listener.Addr().(*net.TCPAddr).Port
						listener.Close()
					}
					opts := &conf.Options{ListenIP: "127.0.0.1", SendIP: "127.0.0.1",
						SingOptions: conf.NewSingOptions(), XrayOptions: conf.NewXrayOptions(),
						CertConfig: &conf.CertConfig{CertMode: "file", CertDomain: "localhost", CertFile: cert, KeyFile: key},
					}
					if err := engine.AddNode(tag, info, opts); err != nil {
						t.Fatal(err)
					}
					users := []panel.UserInfo{
						{Id: 1001, Uuid: "d05c113b-1547-481a-8ac5-3903f56ab14a"},
						{Id: 2002, Uuid: "8211e2bb-3d22-4473-bfd4-a3bc38f12353"},
					}
					if n, err := engine.AddUsers(&core.AddUsersParams{Tag: tag, NodeInfo: info, Users: users}); err != nil || n != 2 {
						t.Fatalf("add users: n=%d err=%v", n, err)
					}
					if err := engine.DelUsers(users[:1], tag, info); err != nil {
						t.Fatal(err)
					}
					if n, err := engine.AddUsers(&core.AddUsersParams{Tag: tag, NodeInfo: info, Users: users[:1]}); err != nil || n != 1 {
						t.Fatalf("re-add user: n=%d err=%v", n, err)
					}
					if _, err := engine.GetUserTrafficSlice(tag, true); err != nil {
						t.Fatal(err)
					}
					if err := engine.DelUsers(users, tag, info); err != nil {
						t.Fatal(err)
					}
					if err := engine.DelNode(tag); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func localCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}
