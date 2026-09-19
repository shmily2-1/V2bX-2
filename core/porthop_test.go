//go:build sing && xray && hysteria2 && with_quic

package core_test

import (
	"net"
	"testing"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/core"
)

// An invalid range fails before any firewall command, on every OS. Confirm
// both real cores roll back the already-created listener and allow a retry.
func TestPortHoppingFailureReleasesListener(t *testing.T) {
	cert, key := localCertificate(t)
	for _, kind := range []string{"sing", "hysteria2"} {
		t.Run(kind, func(t *testing.T) {
			engine, err := core.NewCore([]conf.CoreConfig{{Type: kind, SingConfig: conf.NewSingConfig(), Hysteria2Config: conf.NewHysteria2Config()}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := engine.Close(); err != nil {
					t.Error(err)
				}
			})
			if err = engine.Start(); err != nil {
				t.Fatal(err)
			}
			socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := socket.LocalAddr().(*net.UDPAddr).Port
			address := socket.LocalAddr().String()
			socket.Close()
			info := &panel.NodeInfo{Type: "hysteria2", Security: panel.Tls, Common: &panel.CommonNode{ServerPort: port}, Hysteria2: &panel.Hysteria2Node{Ports: "30000-20000"}}
			opts := &conf.Options{ListenIP: "127.0.0.1", SingOptions: conf.NewSingOptions(), CertConfig: &conf.CertConfig{CertMode: "file", CertFile: cert, KeyFile: key}}
			if err = engine.AddNode("rollback", info, opts); err == nil {
				t.Fatal("expected invalid port range")
			}
			socket, err = net.ListenPacket("udp4", address)
			if err != nil {
				t.Fatal("failed AddNode leaked UDP socket:", err)
			}
			socket.Close()
			info.Hysteria2.Ports = ""
			if err = engine.AddNode("rollback", info, opts); err != nil {
				t.Fatal("retry:", err)
			}
			if err = engine.AddNode("rollback", info, opts); err == nil {
				t.Fatal("duplicate node accepted")
			}
			if err = engine.DelNode("rollback"); err != nil {
				t.Fatal(err)
			}
			if err = engine.AddNode("rollback", info, opts); err != nil {
				t.Fatal("reload:", err)
			}
			if err = engine.Close(); err != nil {
				t.Fatal(err)
			}
			if err = engine.AddNode("after-close", info, opts); err == nil {
				t.Fatal("closed core accepted a new listener")
			}
		})
	}
}
