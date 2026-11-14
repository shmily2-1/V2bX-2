//go:build sing && xray && hysteria2 && with_quic && with_wireguard

package core_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/clientconfig"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/core"
	"github.com/shmily2-1/V2bX-2/limiter"
	xnet "github.com/xtls/xray-core/common/net"
	xcore "github.com/xtls/xray-core/core"
	xconf "github.com/xtls/xray-core/infra/conf"
	"golang.org/x/net/proxy"
)

func extendedNode(t *testing.T, kind string, info *panel.NodeInfo, cert *conf.CertConfig) (core.Core, string, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	info.Common = &panel.CommonNode{ServerPort: port}
	engine, err := core.NewCore([]conf.CoreConfig{{Type: kind, XrayConfig: conf.NewXrayConfig(), SingConfig: conf.NewSingConfig()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	if err = engine.Start(); err != nil {
		t.Fatal(err)
	}
	tag := fmt.Sprintf("extended:http://local:%s:%d", info.Type, port)
	limiter.AddLimiter(tag, &conf.LimitConfig{}, []panel.UserInfo{{Id: 71, Uuid: p1UUID}}, nil)
	t.Cleanup(func() { limiter.DeleteLimiter(tag) })
	opts := &conf.Options{ListenIP: "127.0.0.1", SendIP: "0.0.0.0", CertConfig: cert, SingOptions: conf.NewSingOptions(), XrayOptions: conf.NewXrayOptions()}
	opts.XrayOptions.DisableSniffing = true
	if err = engine.AddNode(tag, info, opts); err != nil {
		t.Fatal(err)
	}
	return engine, tag, net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
}
func httpConnect(address, user, password, target string) (net.Conn, error) {
	c, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(2 * time.Second))
	credential := base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
	if _, err = fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", target, target, credential); err != nil {
		c.Close()
		return nil, err
	}
	r := bufio.NewReader(c)
	line, err := r.ReadString('\n')
	if err != nil {
		c.Close()
		return nil, err
	}
	if !bytes.Contains([]byte(line), []byte(" 200 ")) {
		c.Close()
		return nil, fmt.Errorf("CONNECT rejected")
	}
	for {
		line, err = r.ReadString('\n')
		if err != nil {
			c.Close()
			return nil, err
		}
		if line == "\r\n" {
			break
		}
	}
	return c, nil
}
func echoPayload(c net.Conn) error {
	c.SetDeadline(time.Now().Add(3 * time.Second))
	payload := bytes.Repeat([]byte("real-protocol-roundtrip;"), 64)
	if _, err := c.Write(payload); err != nil {
		return err
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(c, got); err != nil {
		return err
	}
	if !bytes.Equal(payload, got) {
		return fmt.Errorf("echo mismatch")
	}
	return nil
}
func TestExtendedHTTPMixedDataPlane(t *testing.T) {
	limiter.Init()
	target := p1Echo(t)
	for _, kind := range []string{"http", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			info := &panel.NodeInfo{Type: kind, HTTP: &panel.HTTPNode{}}
			engine, tag, address := extendedNode(t, "sing", info, nil)
			if c, err := httpConnect(address, p1UUID, "password", target); err == nil {
				c.Close()
				t.Fatal("empty authorization permitted a connection")
			}
			users := []panel.UserInfo{{Id: 71, Uuid: p1UUID, Password: "test-password-not-a-real-secret"}}
			if _, err := engine.AddUsers(&core.AddUsersParams{Tag: tag, Users: users, NodeInfo: info}); err != nil {
				t.Fatal(err)
			}
			if c, err := httpConnect(address, p1UUID, "wrong", target); err == nil {
				c.Close()
				t.Fatal("wrong password accepted")
			}
			c, err := httpConnect(address, p1UUID, users[0].Password, target)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err = echoPayload(c); err != nil {
				t.Fatal(err)
			}
			if kind == "mixed" {
				dialer, err := proxy.SOCKS5("tcp", address, &proxy.Auth{User: p1UUID, Password: users[0].Password}, &net.Dialer{Timeout: time.Second})
				if err != nil {
					t.Fatal(err)
				}
				socks, err := dialer.Dial("tcp", target)
				if err != nil {
					t.Fatal(err)
				}
				if err = echoPayload(socks); err != nil {
					t.Fatal(err)
				}
				socks.Close()
			}
			waitExtendedTraffic(t, engine, tag)
			rows, err := engine.GetUserTrafficSlice(tag, true)
			if err != nil || len(rows) != 1 || rows[0].UID != 71 || rows[0].Upload == 0 || rows[0].Download == 0 {
				t.Fatalf("missing traffic: %+v %v", rows, err)
			}
			if err = engine.DelUsers(users, tag, info); err != nil {
				t.Fatal(err)
			}
			if err = echoPayload(c); err == nil {
				t.Fatal("revoked CONNECT remained usable")
			}
			if c, err := httpConnect(address, p1UUID, users[0].Password, target); err == nil {
				c.Close()
				t.Fatal("revoked user connected")
			}
			users[0].Password = "changed-test-password"
			if _, err = engine.AddUsers(&core.AddUsersParams{Tag: tag, Users: users, NodeInfo: info}); err != nil {
				t.Fatal(err)
			}
			next, err := httpConnect(address, p1UUID, users[0].Password, target)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			if err = echoPayload(next); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func extendedClient(t *testing.T, document map[string]any) *xcore.Instance {
	t.Helper()
	data, _ := json.Marshal(document)
	var cfg xconf.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	built, err := cfg.Build()
	if err != nil {
		t.Fatal(err)
	}
	c, err := xcore.New(built)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err = c.Start(); err != nil {
		t.Fatal(err)
	}
	return c
}
func clientEcho(c *xcore.Instance, target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	dest, err := xnet.ParseDestination("tcp:" + target)
	if err != nil {
		return err
	}
	conn, err := xcore.Dial(ctx, c, dest)
	if err != nil {
		return err
	}
	defer conn.Close()
	timer := time.AfterFunc(4*time.Second, func() { conn.Close() })
	defer timer.Stop()
	return echoPayload(conn)
}
func TestExtendedXDriveDataPlane(t *testing.T) {
	limiter.Init()
	target := p1Echo(t)
	settings := map[string]any{"service": "local", "remoteFolder": xdriveScratch(t), "segmentBytes": 4096, "flushIntervalMs": 10, "pollIntervalMs": 10, "maxPollIntervalMs": 30, "sessionTtlSeconds": 60, "concurrency": 2}
	raw, _ := json.Marshal(settings)
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	info := &panel.NodeInfo{Type: "vless", VAllss: &panel.VAllssNode{Network: "xdrive", NetworkSettings: raw, Encryption: "mlkem768x25519plus", EncryptionSettings: panel.EncSettings{Mode: "native", Ticket: "600s", PrivateKey: base64.RawURLEncoding.EncodeToString(key.Bytes())}}}
	engine, tag, _ := extendedNode(t, "xray", info, nil)
	users := []panel.UserInfo{{Id: 71, Uuid: p1UUID}}
	if _, err := engine.AddUsers(&core.AddUsersParams{Tag: tag, Users: users, NodeInfo: info}); err != nil {
		t.Fatal(err)
	}
	exported, err := clientconfig.Build(info, nil, users[0], "127.0.0.1", false)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(exported, []byte(info.VAllss.EncryptionSettings.PrivateKey)) {
		t.Fatal("client export contains the server's private key")
	}
	var doc map[string]any
	if err = json.Unmarshal(exported, &doc); err != nil {
		t.Fatal(err)
	}
	doc["log"] = map[string]any{"loglevel": "none"}
	client := extendedClient(t, doc)
	if err := clientEcho(client, target); err != nil {
		t.Fatal(err)
	}
	rows, err := engine.GetUserTrafficSlice(tag, true)
	if err != nil || len(rows) != 1 || rows[0].UID != 71 || rows[0].Upload == 0 || rows[0].Download == 0 {
		t.Fatalf("missing XDRIVE traffic: %+v %v", rows, err)
	}
	if err = engine.DelUsers(users, tag, info); err != nil {
		t.Fatal(err)
	}
	if err = clientEcho(client, target); err == nil {
		t.Fatal("revoked XDRIVE user connected")
	}
}
func TestExtendedMasqueDataPlane(t *testing.T) {
	limiter.Init()
	target := routableEcho(t)
	cert, key := localCertificate(t)
	for _, version := range []string{"h2", "h3"} {
		t.Run(version, func(t *testing.T) {
			info := &panel.NodeInfo{Type: "masque", Security: panel.Tls, Masque: &panel.MasqueNode{Address: []string{"10.194.0.1/24", "fd94::1/120"}, MTU: 1280, Path: "/private-ip"}}
			engine, tag, _ := extendedNode(t, "xray", info, &conf.CertConfig{CertMode: "file", CertFile: cert, KeyFile: key, ALPN: []string{version}})
			users := []panel.UserInfo{{Id: 71, Uuid: p1UUID, Password: "local-masque-test-password"}}
			clientDoc := func(password string) map[string]any {
				data, err := clientconfig.Build(info, &conf.Options{CertConfig: &conf.CertConfig{CertMode: "self", CertDomain: "localhost", CertFile: cert, ALPN: []string{version}}}, panel.UserInfo{Id: 71, Uuid: p1UUID, Password: password}, "127.0.0.1", false)
				if err != nil {
					t.Fatal(err)
				}
				var doc map[string]any
				if err = json.Unmarshal(data, &doc); err != nil {
					t.Fatal(err)
				}
				doc["log"] = map[string]any{"loglevel": "none"}
				return doc
			}
			emptyClient := extendedClient(t, clientDoc(users[0].Password))
			if err := clientEcho(emptyClient, target); err == nil {
				t.Fatal("empty MASQUE authorization accepted")
			}
			emptyClient.Close()
			if _, err := engine.AddUsers(&core.AddUsersParams{Tag: tag, Users: users, NodeInfo: info}); err != nil {
				t.Fatal(err)
			}
			client := extendedClient(t, clientDoc(users[0].Password))
			if err := clientEcho(client, target); err != nil {
				t.Fatal(err)
			}
			rows, err := engine.GetUserTrafficSlice(tag, true)
			if err != nil || len(rows) != 1 || rows[0].UID != 71 || rows[0].Upload == 0 || rows[0].Download == 0 {
				t.Fatalf("missing MASQUE accounting: %+v %v", rows, err)
			}
			if err = engine.DelUsers(users, tag, info); err != nil {
				t.Fatal(err)
			}
			if err = clientEcho(client, target); err == nil {
				t.Fatal("revoked MASQUE tunnel remained usable")
			}
		})
	}
}

func routableEcho(t *testing.T) string {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var l net.Listener
	for _, address := range addresses {
		ip, _, parseErr := net.ParseCIDR(address.String())
		if parseErr == nil && ip.To4() != nil && ip.IsGlobalUnicast() {
			l, err = net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
			if err == nil {
				break
			}
		}
	}
	if l == nil {
		t.Skip("requires a non-loopback local IPv4 address; isolated runner adds 192.0.2.10/32 to lo")
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	return l.Addr().String()
}

func waitExtendedTraffic(t *testing.T, engine core.Core, tag string) {
	t.Helper()
	for until := time.Now().Add(2 * time.Second); time.Now().Before(until); {
		rows, err := engine.GetUserTrafficSlice(tag, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > 0 && rows[0].Upload > 0 && rows[0].Download > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("traffic counters did not observe both directions")
}

func xdriveScratch(t *testing.T) string {
	t.Helper()
	path, err := os.MkdirTemp("", "v2bx-xdrive-")
	if err != nil {
		t.Fatal(err)
	}
	// The pinned listener cancels its background storage workers on Close.
	// Register before engine/client cleanup and allow those workers to drain
	// before reclaiming this exclusively test-owned directory.
	t.Cleanup(func() {
		var last error
		for until := time.Now().Add(2 * time.Second); time.Now().Before(until); {
			if last = os.RemoveAll(path); last == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Errorf("reclaim XDRIVE test storage after shutdown: %v", last)
	})
	return path
}
