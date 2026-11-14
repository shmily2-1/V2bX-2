//go:build sing && xray && hysteria2 && with_quic && with_wireguard

package core_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/clientconfig"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/core"
	"github.com/shmily2-1/V2bX-2/limiter"
)

func TestExportedHTTPMixedClients(t *testing.T) {
	limiter.Init()
	target := p1Echo(t)
	cert, key := localCertificate(t)
	for _, kind := range []string{"http", "mixed"} {
		for _, secure := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-TLS-%v", kind, secure), func(t *testing.T) {
				info := &panel.NodeInfo{Type: kind, HTTP: &panel.HTTPNode{}}
				var serverCert *conf.CertConfig
				if secure {
					info.Security = panel.Tls
					serverCert = &conf.CertConfig{CertMode: "file", CertFile: cert, KeyFile: key}
				}
				engine, tag, _ := extendedNode(t, "sing", info, serverCert)
				user := panel.UserInfo{Id: 71, Uuid: p1UUID, Password: "exported-client-test-password"}
				if _, err := engine.AddUsers(&core.AddUsersParams{Tag: tag, Users: []panel.UserInfo{user}, NodeInfo: info}); err != nil {
					t.Fatal(err)
				}
				data, err := clientconfig.Build(info, &conf.Options{CertConfig: &conf.CertConfig{CertMode: "self", CertFile: cert, CertDomain: "localhost"}}, user, "127.0.0.1", false)
				if err != nil {
					t.Fatal(err)
				}
				var document map[string]any
				if err = json.Unmarshal(data, &document); err != nil {
					t.Fatal(err)
				}
				document["log"] = map[string]any{"loglevel": "none"}
				client := extendedClient(t, document)
				if err = clientEcho(client, target); err != nil {
					t.Fatal(err)
				}
				waitExtendedTraffic(t, engine, tag)
			})
		}
	}
}

func TestExtendedSingleCoreDefaultOptions(t *testing.T) {
	for _, kind := range []string{"sing", "xray"} {
		t.Run(kind, func(t *testing.T) {
			engine, err := core.NewCore([]conf.CoreConfig{{Type: kind, SingConfig: conf.NewSingConfig(), XrayConfig: conf.NewXrayConfig()}})
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			if err = engine.Start(); err != nil {
				t.Fatal(err)
			}
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := l.Addr().(*net.TCPAddr).Port
			l.Close()
			info := &panel.NodeInfo{Common: &panel.CommonNode{ServerPort: port}, Type: "http", HTTP: &panel.HTTPNode{}}
			if kind == "xray" {
				info.Type = "vless"
				info.VAllss = &panel.VAllssNode{Network: "tcp"}
			}
			options := &conf.Options{ListenIP: "127.0.0.1", RawOptions: json.RawMessage(`{"DisableSniffing":true}`)}
			if err = engine.AddNode("default-options", info, options); err != nil {
				t.Fatal(err)
			}
			if err = engine.DelNode("default-options"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func udpEcho(t *testing.T, host string) string {
	t.Helper()
	p, err := net.ListenPacket("udp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	go func() {
		buffer := make([]byte, 65536)
		for {
			n, address, err := p.ReadFrom(buffer)
			if err != nil {
				return
			}
			p.WriteTo(buffer[:n], address)
		}
	}()
	return p.LocalAddr().String()
}
func TestExtendedMixedSOCKSUDP(t *testing.T) {
	limiter.Init()
	target := udpEcho(t, "127.0.0.1")
	info := &panel.NodeInfo{Type: "mixed", HTTP: &panel.HTTPNode{}}
	engine, tag, address := extendedNode(t, "sing", info, nil)
	user := panel.UserInfo{Id: 71, Uuid: p1UUID, Password: "udp-test-password"}
	if _, err := engine.AddUsers(&core.AddUsersParams{Tag: tag, Users: []panel.UserInfo{user}, NodeInfo: info}); err != nil {
		t.Fatal(err)
	}
	ctrl, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()
	ctrl.SetDeadline(time.Now().Add(3 * time.Second))
	ctrl.Write([]byte{5, 1, 2})
	reply := make([]byte, 2)
	if _, err = io.ReadFull(ctrl, reply); err != nil || !bytes.Equal(reply, []byte{5, 2}) {
		t.Fatal("SOCKS authentication method", err)
	}
	credential := append([]byte{1, byte(len(user.Uuid))}, []byte(user.Uuid)...)
	credential = append(credential, byte(len(user.Password)))
	credential = append(credential, []byte(user.Password)...)
	ctrl.Write(credential)
	if _, err = io.ReadFull(ctrl, reply); err != nil || !bytes.Equal(reply, []byte{1, 0}) {
		t.Fatal("SOCKS authentication", err)
	}
	ctrl.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	header := make([]byte, 4)
	if _, err = io.ReadFull(ctrl, header); err != nil || header[1] != 0 {
		t.Fatal("UDP associate", err)
	}
	size := 4
	if header[3] == 4 {
		size = 16
	} else if header[3] != 1 {
		t.Fatal("unexpected UDP relay address")
	}
	bind := make([]byte, size+2)
	if _, err = io.ReadFull(ctrl, bind); err != nil {
		t.Fatal(err)
	}
	port := binary.BigEndian.Uint16(bind[size:])
	relay := fmt.Sprintf("127.0.0.1:%d", port)
	c, err := net.DialTimeout("udp", relay, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	dest, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("authenticated-udp;"), 32)
	packet := append([]byte{0, 0, 0, 1}, dest.IP.To4()...)
	packet = binary.BigEndian.AppendUint16(packet, uint16(dest.Port))
	packet = append(packet, payload...)
	if _, err = c.Write(packet); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 65536)
	n, err := c.Read(response)
	if err != nil || n < len(payload)+10 || !bytes.Equal(response[n-len(payload):n], payload) {
		t.Fatal("SOCKS UDP roundtrip failed", err)
	}
	waitExtendedTraffic(t, engine, tag)
	if err = engine.DelUsers([]panel.UserInfo{user}, tag, info); err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(200 * time.Millisecond))
	c.Write(packet)
	if _, err = c.Read(response); err == nil {
		t.Fatal("revoked UDP association remained usable")
	}
}
