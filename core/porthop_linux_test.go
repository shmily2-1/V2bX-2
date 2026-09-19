//go:build linux && sing && xray && hysteria2 && with_quic

package core_test

import (
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	hyclient "github.com/apernet/hysteria/core/v2/client"
	"github.com/apernet/hysteria/extras/v2/transport/udphop"
	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/common/porthop"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/core"
	"github.com/shmily2-1/V2bX-2/limiter"
)

type hopFactory struct {
	mu   sync.Mutex
	seen map[int]bool
}
type observedUDP struct {
	net.PacketConn
	factory *hopFactory
}

// udphop exposes syscall.Conn to quic-go. Preserve the underlying UDP socket
// interface when observing destinations rather than breaking client setup.
func (c *observedUDP) SyscallConn() (syscall.RawConn, error) {
	return c.PacketConn.(syscall.Conn).SyscallConn()
}

func (c *observedUDP) WriteTo(p []byte, a net.Addr) (int, error) {
	c.factory.mu.Lock()
	c.factory.seen[a.(*net.UDPAddr).Port] = true
	c.factory.mu.Unlock()
	return c.PacketConn.WriteTo(p, a)
}
func (f *hopFactory) New(a net.Addr) (net.PacketConn, error) {
	return udphop.NewUDPHopPacketConn(a.(*udphop.UDPHopAddr), udphop.HopIntervalConfig{Min: 5 * time.Second, Max: 5 * time.Second}, func() (net.PacketConn, error) {
		c, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		return &observedUDP{c, f}, nil
	})
}

// Explicit opt-in ONLY inside a disposable Linux network namespace. The
// ordinary test suite skips this test and never changes firewall state.
func TestPortHoppingNetNS(t *testing.T) {
	if os.Getenv("V2BX_TEST_NETNS") != "1" {
		t.Skip("requires explicit opt-in inside an isolated network namespace")
	}
	selfNS, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	initNS, err := os.Readlink("/proc/1/ns/net")
	if err != nil || selfNS == initNS {
		t.Fatal("refusing to change the init/host network namespace")
	}
	if os.Geteuid() != 0 {
		t.Fatal("namespace test requires root/CAP_NET_ADMIN")
	}
	backend := os.Getenv(porthop.BackendEnv)
	if backend != "nftables" && backend != "iptables" {
		t.Fatal("select explicit test backend")
	}
	cert, key := localCertificate(t)
	pem, err := os.ReadFile(cert)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem)
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); c.SetDeadline(time.Now().Add(25 * time.Second)); io.Copy(c, c) }()
		}
	}()
	limiter.Init()
	for _, kind := range []string{"hysteria2", "sing"} {
		t.Run(kind, func(t *testing.T) {
			engine, err := core.NewCore([]conf.CoreConfig{{Type: kind, SingConfig: conf.NewSingConfig(), Hysteria2Config: conf.NewHysteria2Config()}})
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			if err = engine.Start(); err != nil {
				t.Fatal(err)
			}
			info := &panel.NodeInfo{Type: "hysteria2", Security: panel.Tls, Common: &panel.CommonNode{ServerPort: 18443}, Hysteria2: &panel.Hysteria2Node{Ports: "20000-30000", HopInterval: 5}}
			opts := &conf.Options{ListenIP: "::", SingOptions: conf.NewSingOptions(), CertConfig: &conf.CertConfig{CertMode: "file", CertFile: cert, KeyFile: key}}
			users := []panel.UserInfo{{Id: 9001, Uuid: "d05c113b-1547-481a-8ac5-3903f56ab14a"}}
			limiter.AddLimiter(kind, &conf.LimitConfig{}, users, map[int]int{})
			defer limiter.DeleteLimiter(kind)
			if err = engine.AddNode(kind, info, opts); err != nil {
				t.Fatal(err)
			}
			if _, err = engine.AddUsers(&core.AddUsersParams{Tag: kind, NodeInfo: info, Users: users}); err != nil {
				t.Fatal(err)
			}
			// Test both ends and an interior public port, not just server_port.
			for _, endpoint := range []struct {
				ip   string
				port int
			}{{"127.0.0.1", 20000}, {"127.0.0.1", 25000}, {"127.0.0.1", 30000}, {"::1", 20000}, {"::1", 30000}} {
				a := &net.UDPAddr{IP: net.ParseIP(endpoint.ip), Port: endpoint.port}
				client, _, err := hyclient.NewClient(&hyclient.Config{ServerAddr: a, Auth: users[0].Uuid, TLSConfig: hyclient.TLSConfig{ServerName: "localhost", RootCAs: roots}})
				if err != nil {
					t.Fatalf("public endpoint %v: %v", a, err)
				}
				c, err := client.TCP(echo.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				hopEcho(t, c)
				c.Close()
				client.Close()
			}
			addr, err := udphop.ResolveUDPHopAddr("127.0.0.1:20000-30000")
			if err != nil {
				t.Fatal(err)
			}
			factory := &hopFactory{seen: make(map[int]bool)}
			client, _, err := hyclient.NewClient(&hyclient.Config{ConnFactory: factory, ServerAddr: addr, Auth: users[0].Uuid, TLSConfig: hyclient.TLSConfig{ServerName: "localhost", RootCAs: roots}})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			c, err := client.TCP(echo.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			deadline := time.Now().Add(17 * time.Second)
			for {
				hopEcho(t, c)
				factory.mu.Lock()
				seen := len(factory.seen)
				factory.mu.Unlock()
				if seen >= 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("client did not use multiple destination ports")
				}
				time.Sleep(200 * time.Millisecond)
			}
			factory.mu.Lock()
			t.Logf("same TCP stream survived real QUIC port hopping: %v", factory.seen)
			factory.mu.Unlock()
			c.Close()
			client.Close()
			traffic, err := engine.GetUserTrafficSlice(kind, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(traffic) != 1 || traffic[0].UID != 9001 || traffic[0].Upload == 0 || traffic[0].Download == 0 {
				t.Fatalf("traffic: %+v", traffic)
			}
			if err = engine.DelNode(kind); err != nil {
				t.Fatal(err)
			}
			assertNoHopRules(t, backend)
			info.Hysteria2.Ports = "21000-21010"
			if err = engine.AddNode(kind, info, opts); err != nil {
				t.Fatal("reload:", err)
			}
			if err = engine.Close(); err != nil {
				t.Fatal(err)
			}
			assertNoHopRules(t, backend)
		})
	}
}

func hopEcho(t *testing.T, c net.Conn) {
	t.Helper()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("real-port-hop-echo")
	if _, err := c.Write(payload); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != string(payload) {
		t.Fatal("echo mismatch")
	}
}
func assertNoHopRules(t *testing.T, backend string) {
	t.Helper()
	var commands [][]string
	if backend == "nftables" {
		commands = [][]string{{"nft", "list", "tables"}}
	} else {
		commands = [][]string{{"iptables-save", "-t", "nat"}, {"ip6tables-save", "-t", "nat"}}
	}
	for _, args := range commands {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatal(fmt.Errorf("%v: %w %s", args, err, out))
		}
		if strings.Contains(string(out), "v2bx_hop_") || strings.Contains(string(out), "V2BX-HOP-") {
			t.Fatalf("leaked rules: %s", out)
		}
	}
}
