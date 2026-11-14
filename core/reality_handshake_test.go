//go:build sing && xray && hysteria2 && with_quic && with_utls

package core_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	singTLS "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	vlessclient "github.com/sagernet/sing-vmess/vless"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/core"
	"github.com/shmily2-1/V2bX-2/limiter"
)

// REALITY camouflage, proxy target and V2bX servers all bind to loopback.
// No public target, real user, panel or production certificate is used.
func TestRealityHandshakeInteroperability(t *testing.T) {
	certPath, keyPath := localCertificate(t)
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	limiter.Init()
	for _, targetCurve := range []tls.CurveID{tls.X25519MLKEM768, tls.X25519} {
		for _, serverCore := range []string{"xray", "sing"} {
			t.Run(serverCore+"-"+strconv.Itoa(int(targetCurve)), func(t *testing.T) {
				target, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
					Certificates:     []tls.Certificate{certificate},
					MinVersion:       tls.VersionTLS13,
					MaxVersion:       tls.VersionTLS13,
					CurvePreferences: []tls.CurveID{targetCurve},
					NextProtos:       []string{"h2", "http/1.1"},
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { target.Close() })
				go serveRealityTestEcho(target)
				echo, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { echo.Close() })
				go serveRealityTestEcho(echo)

				key, err := ecdh.X25519().GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				port := listener.Addr().(*net.TCPAddr).Port
				listener.Close()
				targetPort := target.Addr().(*net.TCPAddr).Port
				info := &panel.NodeInfo{
					Type: "vless", Security: panel.Reality,
					Common: &panel.CommonNode{ServerPort: port},
					VAllss: &panel.VAllssNode{
						Network: "tcp",
						TlsSettings: panel.TlsSettings{
							ServerName: "localhost", Dest: "127.0.0.1", ServerPort: strconv.Itoa(targetPort),
							PrivateKey: base64.RawURLEncoding.EncodeToString(key.Bytes()), ShortId: "1234",
						},
					},
				}
				engine, err := core.NewCore([]conf.CoreConfig{{
					Type: serverCore, SingConfig: conf.NewSingConfig(), XrayConfig: conf.NewXrayConfig(),
				}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { engine.Close() })
				if err := engine.Start(); err != nil {
					t.Fatal(err)
				}
				tag := "reality-" + serverCore
				users := []panel.UserInfo{{Id: 9021, Uuid: "d05c113b-1547-481a-8ac5-3903f56ab14a"}}
				limiter.AddLimiter(tag, &conf.LimitConfig{}, users, map[int]int{})
				t.Cleanup(func() { limiter.DeleteLimiter(tag) })
				if err := engine.AddNode(tag, info, &conf.Options{
					ListenIP: "127.0.0.1", SendIP: "127.0.0.1",
					SingOptions: conf.NewSingOptions(), XrayOptions: conf.NewXrayOptions(),
				}); err != nil {
					t.Fatal(err)
				}
				if _, err := engine.AddUsers(&core.AddUsersParams{Tag: tag, NodeInfo: info, Users: users}); err != nil {
					t.Fatal(err)
				}
				config, err := singTLS.NewRealityClient(context.Background(), nil, "localhost", option.OutboundTLSOptions{
					Enabled: true, ServerName: "localhost",
					UTLS: &option.OutboundUTLSOptions{Enabled: true, Fingerprint: "chrome"},
					Reality: &option.OutboundRealityOptions{
						Enabled: true, PublicKey: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), ShortID: "1234",
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				roundTrip := func(uuid string) error {
					raw, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 3*time.Second)
					if err != nil {
						return err
					}
					defer raw.Close()
					raw.SetDeadline(time.Now().Add(5 * time.Second))
					conn, err := singTLS.ClientHandshake(context.Background(), raw, config)
					if err != nil {
						return err
					}
					defer conn.Close()
					client, err := vlessclient.NewClient(uuid, "", nil)
					if err != nil {
						return err
					}
					proxy, err := client.DialConn(conn, M.ParseSocksaddr(echo.Addr().String()))
					if err != nil {
						return err
					}
					defer proxy.Close()
					payload := []byte("local-reality-authenticated-echo")
					if _, err := proxy.Write(payload); err != nil {
						return err
					}
					actual := make([]byte, len(payload))
					if _, err := io.ReadFull(proxy, actual); err != nil {
						return err
					}
					if !bytes.Equal(actual, payload) {
						return io.ErrUnexpectedEOF
					}
					return nil
				}
				if err := roundTrip(users[0].Uuid); err != nil {
					t.Fatal("authenticated REALITY+VLESS echo:", err)
				}
				if err := roundTrip("8211e2bb-3d22-4473-bfd4-a3bc38f12353"); err == nil {
					t.Fatal("unregistered VLESS user passed authentication")
				}
			})
		}
	}
}

func serveRealityTestEcho(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(10 * time.Second))
			io.Copy(conn, conn)
		}()
	}
}
