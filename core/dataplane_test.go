//go:build sing && xray && hysteria2 && with_quic

package core_test

import (
	"bytes"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	hyclient "github.com/apernet/hysteria/core/v2/client"
	vlessclient "github.com/sagernet/sing-vmess/vless"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/core"
	"github.com/shmily2-1/V2bX-2/limiter"
)

// Real local TCP/QUIC traffic: authentication, echo, accounting and revocation.
func TestUpgradedCoreDataPlane(t *testing.T) {
	cert, key := localCertificate(t)
	certPEM, err := os.ReadFile(cert)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("invalid test certificate")
	}
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { echo.Close() })
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); c.SetDeadline(time.Now().Add(10 * time.Second)); io.Copy(c, c) }()
		}
	}()
	limiter.Init()
	for _, tc := range []struct{ engine, protocol string }{
		{"xray", "vless"}, {"sing", "vless"}, {"sing", "hysteria2"}, {"hysteria2", "hysteria2"},
	} {
		t.Run(tc.engine+"-"+tc.protocol, func(t *testing.T) {
			cfg := conf.CoreConfig{Type: tc.engine, SingConfig: conf.NewSingConfig(), XrayConfig: conf.NewXrayConfig(), Hysteria2Config: conf.NewHysteria2Config()}
			engine, err := core.NewCore([]conf.CoreConfig{cfg})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { engine.Close() })
			if err := engine.Start(); err != nil {
				t.Fatal(err)
			}
			var port int
			if tc.protocol == "hysteria2" {
				c, err := net.ListenPacket("udp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				port = c.LocalAddr().(*net.UDPAddr).Port
				c.Close()
			} else {
				c, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				port = c.Addr().(*net.TCPAddr).Port
				c.Close()
			}
			address := fmt.Sprintf("127.0.0.1:%d", port)
			info := &panel.NodeInfo{Type: tc.protocol, Common: &panel.CommonNode{ServerPort: port},
				VAllss: &panel.VAllssNode{Network: "tcp"}, Hysteria2: &panel.Hysteria2Node{}}
			opts := &conf.Options{ListenIP: "127.0.0.1", SendIP: "127.0.0.1", XrayOptions: conf.NewXrayOptions(), SingOptions: conf.NewSingOptions(),
				CertConfig: &conf.CertConfig{CertMode: "file", CertDomain: "localhost", CertFile: cert, KeyFile: key}}
			if tc.protocol == "hysteria2" {
				info.Security = panel.Tls
			}
			users := []panel.UserInfo{{Id: 9001, Uuid: "d05c113b-1547-481a-8ac5-3903f56ab14a"}}
			tag := tc.engine + "-" + tc.protocol
			limiter.AddLimiter(tag, &conf.LimitConfig{}, users, map[int]int{})
			t.Cleanup(func() { limiter.DeleteLimiter(tag) })
			if err := engine.AddNode(tag, info, opts); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.AddUsers(&core.AddUsersParams{Tag: tag, NodeInfo: info, Users: users}); err != nil {
				t.Fatal(err)
			}
			roundTrip := func() error {
				var c net.Conn
				if tc.protocol == "hysteria2" {
					addr, _ := net.ResolveUDPAddr("udp", address)
					client, _, err := hyclient.NewClient(&hyclient.Config{ServerAddr: addr, Auth: users[0].Uuid,
						TLSConfig: hyclient.TLSConfig{ServerName: "localhost", RootCAs: roots}})
					if err != nil {
						return err
					}
					defer client.Close()
					c, err = client.TCP(echo.Addr().String())
					if err != nil {
						return err
					}
				} else {
					raw, err := net.DialTimeout("tcp", address, 3*time.Second)
					if err != nil {
						return err
					}
					defer raw.Close()
					raw.SetDeadline(time.Now().Add(3 * time.Second))
					client, err := vlessclient.NewClient(users[0].Uuid, "", nil)
					if err != nil {
						return err
					}
					c, err = client.DialConn(raw, M.ParseSocksaddr(echo.Addr().String()))
					if err != nil {
						return err
					}
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				payload := bytes.Repeat([]byte("local-core-upgrade-echo;"), 16)
				if _, err := c.Write(payload); err != nil {
					return err
				}
				actual := make([]byte, len(payload))
				if _, err := io.ReadFull(c, actual); err != nil {
					return err
				}
				if !bytes.Equal(actual, payload) {
					return fmt.Errorf("echo payload mismatch")
				}
				return nil
			}
			if err := roundTrip(); err != nil {
				t.Fatal("authenticated traffic:", err)
			}
			var accounted bool
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
				traffic, err := engine.GetUserTrafficSlice(tag, false)
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range traffic {
					if row.UID == 9001 && row.Upload > 0 && row.Download > 0 {
						accounted = true
					}
				}
				if accounted {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !accounted {
				t.Fatal("missing per-user bidirectional traffic accounting")
			}
			if err := engine.DelUsers(users, tag, info); err != nil {
				t.Fatal(err)
			}
			if err := roundTrip(); err == nil {
				t.Fatal("removed user was still authenticated")
			}
		})
	}
}
