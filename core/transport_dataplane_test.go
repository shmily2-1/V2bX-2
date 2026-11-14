//go:build sing && xray && hysteria2 && with_quic

package core_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/trojan"
	"github.com/sagernet/sing-box/transport/v2rayhttpupgrade"
	vlessclient "github.com/sagernet/sing-vmess/vless"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/core"
	"github.com/shmily2-1/V2bX-2/limiter"
	xnet "github.com/xtls/xray-core/common/net"
	xcore "github.com/xtls/xray-core/core"
	xconf "github.com/xtls/xray-core/infra/conf"
)

const p1UUID = "0b6f44ef-1e88-4598-bb15-6da2ff80230a"

func p1Echo(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return listener.Addr().String()
}

func p1Node(t *testing.T, kind, protocol, transport string, settings json.RawMessage, certificate *conf.CertConfig, limits *conf.LimitConfig) (core.Core, *panel.NodeInfo, string, string, []panel.UserInfo) {
	t.Helper()
	var port int
	if transport == "kcp" {
		packet, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port = packet.LocalAddr().(*net.UDPAddr).Port
		packet.Close()
	} else {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port = listener.Addr().(*net.TCPAddr).Port
		listener.Close()
	}
	engine, err := core.NewCore([]conf.CoreConfig{{Type: kind, XrayConfig: conf.NewXrayConfig(), SingConfig: conf.NewSingConfig()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	info := &panel.NodeInfo{Type: protocol, Common: &panel.CommonNode{ServerPort: port}, VAllss: &panel.VAllssNode{Network: transport, NetworkSettings: settings}, Trojan: &panel.TrojanNode{Network: transport, NetworkSettings: settings}}
	if certificate != nil && certificate.CertMode != "none" {
		info.Security = panel.Tls
	}
	opts := &conf.Options{ListenIP: "127.0.0.1", SendIP: "127.0.0.1", CertConfig: certificate, XrayOptions: conf.NewXrayOptions(), SingOptions: conf.NewSingOptions()}
	opts.XrayOptions.DisableSniffing = true
	tag := "p1-" + kind + "-" + protocol + "-" + transport
	users := []panel.UserInfo{{Id: 9231, Uuid: p1UUID}}
	limiter.AddLimiter(tag, limits, users, nil)
	t.Cleanup(func() { limiter.DeleteLimiter(tag) })
	if err := engine.AddNode(tag, info, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.AddUsers(&core.AddUsersParams{Tag: tag, Users: users, NodeInfo: info}); err != nil {
		t.Fatal(err)
	}
	return engine, info, tag, net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), users
}

func p1XrayRoundTrip(t *testing.T, protocol, transport string, settings json.RawMessage, port int, user, target string) error {
	t.Helper()
	var transportOptions map[string]any
	if err := json.Unmarshal(settings, &transportOptions); err != nil {
		return err
	}
	key := map[string]string{"kcp": "kcpSettings", "xhttp": "xhttpSettings", "httpupgrade": "httpupgradeSettings"}[transport]
	var credentials any
	if protocol == "vless" {
		credentials = map[string]any{"vnext": []any{map[string]any{"address": "127.0.0.1", "port": port, "users": []any{map[string]any{"id": user, "encryption": "none"}}}}}
	} else {
		credentials = map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": port, "password": user}}}
	}
	document := map[string]any{"log": map[string]any{"loglevel": "none"}, "outbounds": []any{map[string]any{"protocol": protocol, "settings": credentials, "streamSettings": map[string]any{"network": transport, key: transportOptions}}}}
	data, _ := json.Marshal(document)
	var config xconf.Config
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	built, err := config.Build()
	if err != nil {
		return err
	}
	client, err := xcore.New(built)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Start(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	destination, err := xnet.ParseDestination("tcp:" + target)
	if err != nil {
		return err
	}
	conn, err := xcore.Dial(ctx, client, destination)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.AfterFunc(4*time.Second, func() { conn.Close() })
	defer deadline.Stop()
	payload := bytes.Repeat([]byte("p1-local-authenticated-echo;"), 32)
	if _, err := conn.Write(payload); err != nil {
		return err
	}
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, response); err != nil {
		return err
	}
	if !bytes.Equal(payload, response) {
		return fmt.Errorf("echo mismatch")
	}
	return nil
}

func TestAdaptedTransportDataPlane(t *testing.T) {
	limiter.Init()
	target := p1Echo(t)
	for _, tc := range []struct{ kind, protocol, transport, settings string }{
		{"xray", "vless", "kcp", `{"mtu":1200,"tti":10,"uplinkCapacity":20,"downlinkCapacity":20}`},
		{"xray", "vless", "xhttp", `{"path":"/p1","mode":"packet-up"}`},
		{"xray", "trojan", "httpupgrade", `{"path":"/p1","host":"localhost"}`},
		{"sing", "trojan", "httpupgrade", `{"path":"/p1","host":"localhost"}`},
		{"xray", "trojan", "xhttp", `{"path":"/p1","mode":"packet-up"}`},
	} {
		t.Run(tc.kind+"-"+tc.protocol+"-"+tc.transport, func(t *testing.T) {
			engine, info, tag, _, users := p1Node(t, tc.kind, tc.protocol, tc.transport, json.RawMessage(tc.settings), nil, &conf.LimitConfig{})
			roundTrip := func() error {
				if tc.kind == "sing" {
					return p1SingUpgradeRoundTrip(info.Common.ServerPort, p1UUID, target)
				}
				return p1XrayRoundTrip(t, tc.protocol, tc.transport, json.RawMessage(tc.settings), info.Common.ServerPort, p1UUID, target)
			}
			if err := roundTrip(); err != nil {
				t.Fatal(err)
			}
			rows, err := engine.GetUserTrafficSlice(tag, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].UID != 9231 || rows[0].Upload == 0 || rows[0].Download == 0 {
				t.Fatalf("missing accounting: %+v", rows)
			}
			if err := engine.DelUsers(users, tag, info); err != nil {
				t.Fatal(err)
			}
			if err := roundTrip(); err == nil {
				t.Fatal("revoked user connected")
			}
		})
	}
}

type p1NativeDialer struct{}

func (p1NativeDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, destination.String())
}
func (p1NativeDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, fmt.Errorf("not used by HTTPUpgrade")
}

func p1SingUpgradeRoundTrip(port int, password, target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	client, err := v2rayhttpupgrade.NewClient(ctx, p1NativeDialer{}, M.ParseSocksaddr(net.JoinHostPort("127.0.0.1", fmt.Sprint(port))), option.V2RayHTTPUpgradeOptions{Path: "/p1", Host: "localhost"}, nil)
	if err != nil {
		return err
	}
	defer client.Close()
	raw, err := client.DialContext(ctx)
	if err != nil {
		return err
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(4 * time.Second))
	conn := trojan.NewClientConn(raw, trojan.Key(password), M.ParseSocksaddr(target))
	payload := bytes.Repeat([]byte("sing-httpupgrade-echo;"), 32)
	if _, err := conn.Write(payload); err != nil {
		return err
	}
	actual := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, actual); err != nil {
		return err
	}
	if !bytes.Equal(payload, actual) {
		return fmt.Errorf("HTTPUpgrade payload mismatch")
	}
	return nil
}

func p1ECHKeys(t *testing.T) (string, []byte) {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte{1}
	content = binary.BigEndian.AppendUint16(content, 0x0020)
	content = binary.BigEndian.AppendUint16(content, 32)
	content = append(content, key.PublicKey().Bytes()...)
	content = binary.BigEndian.AppendUint16(content, 4)
	content = binary.BigEndian.AppendUint16(content, 1)
	content = binary.BigEndian.AppendUint16(content, 1)
	content = append(content, 0, byte(len("public.test")))
	content = append(content, []byte("public.test")...)
	content = binary.BigEndian.AppendUint16(content, 0)
	config := binary.BigEndian.AppendUint16(nil, 0xfe0d)
	config = binary.BigEndian.AppendUint16(config, uint16(len(content)))
	config = append(config, content...)
	private := binary.BigEndian.AppendUint16(nil, 32)
	private = append(private, key.Bytes()...)
	private = binary.BigEndian.AppendUint16(private, uint16(len(config)))
	private = append(private, config...)
	path := filepath.Join(t.TempDir(), "ech.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "ECH KEYS", Bytes: private}), 0600); err != nil {
		t.Fatal(err)
	}
	list := binary.BigEndian.AppendUint16(nil, uint16(len(config)))
	return path, append(list, config...)
}

func TestConfiguredECHHandshake(t *testing.T) {
	limiter.Init()
	cert, key := localCertificate(t)
	certificate, _ := os.ReadFile(cert)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certificate)
	echFile, configList := p1ECHKeys(t)
	for _, kind := range []string{"xray", "sing"} {
		t.Run(kind, func(t *testing.T) {
			_, _, _, address, _ := p1Node(t, kind, "vless", "tcp", nil, &conf.CertConfig{CertMode: "file", CertFile: cert, KeyFile: key, CertDomain: "localhost", MinVersion: "1.3", ECHKeyFile: echFile}, &conf.LimitConfig{})
			conn, err := tls.Dial("tcp", address, &tls.Config{ServerName: "localhost", RootCAs: roots, MinVersion: tls.VersionTLS13, EncryptedClientHelloConfigList: configList})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if !conn.ConnectionState().ECHAccepted {
				t.Fatal("ECH configuration was not applied")
			}
		})
	}
}

func TestConfiguredTLSNegotiation(t *testing.T) {
	limiter.Init()
	cert, key := localCertificate(t)
	pem, err := os.ReadFile(cert)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem)
	target := p1Echo(t)
	for _, kind := range []string{"xray", "sing"} {
		t.Run(kind, func(t *testing.T) {
			certificate := &conf.CertConfig{CertMode: "file", CertDomain: "localhost", CertFile: cert, KeyFile: key, ALPN: []string{"p1-test"}, MinVersion: "1.2", MaxVersion: "1.2", CipherSuites: []string{"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256"}}
			_, _, _, address, _ := p1Node(t, kind, "vless", "tcp", nil, certificate, &conf.LimitConfig{})
			conn, err := tls.Dial("tcp", address, &tls.Config{ServerName: "localhost", RootCAs: roots, NextProtos: []string{"p1-test"}, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			state := conn.ConnectionState()
			if state.NegotiatedProtocol != "p1-test" || state.Version != tls.VersionTLS12 || state.CipherSuite != tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256 {
				t.Fatalf("TLS settings not applied: %+v", state)
			}
			client, _ := vlessclient.NewClient(p1UUID, "", nil)
			proxy, err := client.DialConn(conn, M.ParseSocksaddr(target))
			if err != nil {
				t.Fatal(err)
			}
			defer proxy.Close()
			proxy.SetDeadline(time.Now().Add(3 * time.Second))
			payload := bytes.Repeat([]byte("tls-data;"), 64)
			if _, err := proxy.Write(payload); err != nil {
				t.Fatal(err)
			}
			actual := make([]byte, len(payload))
			if _, err := io.ReadFull(proxy, actual); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(payload, actual) {
				t.Fatal("TLS proxy payload mismatch")
			}
			bad, err := tls.Dial("tcp", address, &tls.Config{ServerName: "localhost", RootCAs: roots, MinVersion: tls.VersionTLS13})
			if err == nil {
				bad.Close()
				t.Fatal("ignored configured TLS maximum version")
			}
		})
	}
}

func TestDynamicLimitAffectsAnExistingConnection(t *testing.T) {
	limiter.Init()
	target := p1Echo(t)
	for _, kind := range []string{"xray", "sing"} {
		t.Run(kind, func(t *testing.T) {
			_, _, tag, address, _ := p1Node(t, kind, "vless", "tcp", nil, nil, &conf.LimitConfig{EnableDynamicSpeedLimit: true})
			raw, err := net.DialTimeout("tcp", address, 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			raw.SetDeadline(time.Now().Add(10 * time.Second))
			client, _ := vlessclient.NewClient(p1UUID, "", nil)
			conn, err := client.DialConn(raw, M.ParseSocksaddr(target))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			warmup := bytes.Repeat([]byte("warmup;"), 128)
			if _, err := conn.Write(warmup); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(conn, make([]byte, len(warmup))); err != nil {
				t.Fatal(err)
			}
			limit, err := limiter.GetLimiter(tag)
			if err != nil {
				t.Fatal(err)
			}
			if err := limit.UpdateDynamicSpeedLimit(tag, p1UUID, 1, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			payload := bytes.Repeat([]byte("q"), 256*1024)
			start := time.Now()
			if _, err := conn.Write(payload); err != nil {
				t.Fatal(err)
			}
			response := make([]byte, len(payload))
			if _, err := io.ReadFull(conn, response); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(payload, response) {
				t.Fatal("dynamic limit corrupted stream")
			}
			if elapsed := time.Since(start); elapsed < time.Second {
				t.Fatalf("existing stream ignored dynamic limit: %s", elapsed)
			}
		})
	}
}
