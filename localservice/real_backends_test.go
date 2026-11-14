//go:build linux

package localservice

import (
	"context"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shmily2-1/V2bX-2/conf"
)

func realBackend(t *testing.T, variable, version string) *conf.BackendService {
	t.Helper()
	path := os.Getenv(variable)
	if os.Getenv("V2BX_ISOLATED_TEST") != "1" || path == "" {
		t.Skip("requires an explicitly provisioned backend and isolated Linux network/mount namespaces")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return &conf.BackendService{Executable: path, Version: version, SHA256: hex.EncodeToString(digest[:])}
}
func realKey(t *testing.T) (string, string) {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k.Bytes()), base64.StdEncoding.EncodeToString(k.PublicKey().Bytes())
}
func systemIP(t *testing.T, args ...string) {
	t.Helper()
	if output, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
		t.Fatalf("ip %s: %s (%v)", strings.Join(args, " "), output, err)
	}
}
func TestRealAmneziaWGBackend(t *testing.T) {
	b := realBackend(t, "V2BX_AWG_BINARY", "amneziawg-go-v3.1.20260828")
	root := t.TempDir()
	serverPrivate, serverPublic := realKey(t)
	clientPrivate, clientPublic := realKey(t)
	keyfile := filepath.Join(root, "server.key")
	if err := WritePrivate(keyfile, []byte(serverPrivate)); err != nil {
		t.Fatal(err)
	}
	b.Interface = "v2bxawg"
	b.ControlSocket = "/var/run/amneziawg/v2bxawg.sock"
	b.Obfuscation = map[string]string{"jc": "4", "jmin": "12", "jmax": "20", "s1": "17", "s2": "23", "h1": "123451", "h2": "123452", "h3": "123453", "h4": "123454"}
	s := conf.LocalService{ID: "real-awg", Type: "amneziawg", Enabled: true, Listen: "0.0.0.0:51947", Backend: b, WireGuard: &conf.WireGuardService{PrivateKeyFile: keyfile, Address: []string{"10.196.0.1/24"}, Endpoint: "198.19.0.1:51947", MTU: 1280, Peers: []conf.WireGuardPeer{{UID: 301, PublicKey: clientPublic, Address: []string{"10.196.0.2/32"}}}}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	ns := "v2bx-awg-client"
	systemIP(t, "netns", "add", ns)
	t.Cleanup(func() { exec.Command("ip", "netns", "del", ns).Run() })
	systemIP(t, "link", "add", "awgouter", "type", "veth", "peer", "name", "awgp")
	systemIP(t, "link", "set", "awgp", "netns", ns)
	systemIP(t, "address", "add", "198.19.0.1/30", "dev", "awgouter")
	systemIP(t, "link", "set", "awgouter", "up")
	systemIP(t, "-n", ns, "link", "set", "lo", "up")
	systemIP(t, "-n", ns, "address", "add", "198.19.0.2/30", "dev", "awgp")
	systemIP(t, "-n", ns, "link", "set", "awgp", "up")
	instance, err := Open(s, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { instance.Close() })
	client := exec.Command("ip", "netns", "exec", ns, b.Executable, "-f", "awgclient")
	client.Stdout, client.Stderr = io.Discard, io.Discard
	if err = client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Process.Signal(os.Interrupt); client.Wait() })
	clientSocket := "/var/run/amneziawg/awgclient.sock"
	ready := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if _, err = uapi(clientSocket, "get", ""); err == nil {
			ready = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		t.Fatal("client UAPI unavailable")
	}
	text := "private_key=" + wgHex(clientPrivate) + "\n"
	for k, v := range b.Obfuscation {
		text += k + "=" + v + "\n"
	}
	text += "public_key=" + wgHex(serverPublic) + "\nendpoint=198.19.0.1:51947\nallowed_ip=10.196.0.1/32\npersistent_keepalive_interval=1\n"
	if _, err = uapi(clientSocket, "set", text); err != nil {
		t.Fatal(err)
	}
	systemIP(t, "-n", ns, "address", "add", "10.196.0.2/32", "dev", "awgclient")
	systemIP(t, "-n", ns, "link", "set", "awgclient", "mtu", "1280", "up")
	systemIP(t, "-n", ns, "route", "add", "10.196.0.1/32", "dev", "awgclient")
	listener, err := net.Listen("tcp", "10.196.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	program := fmt.Sprintf("import socket\ns=socket.create_connection(('10.196.0.1',%d),timeout=3)\ns.settimeout(3)\ndata=b'real-amneziawg-encrypted-transfer'*64\ns.sendall(data)\nr=b''\nwhile len(r)<len(data): r+=s.recv(len(data)-len(r))\nassert r==data\ns.close()", port)
	if output, err := exec.Command("ip", "netns", "exec", ns, "python3", "-c", program).CombinedOutput(); err != nil {
		t.Fatalf("actual AmneziaWG transfer failed: %s (%v)", output, err)
	}
	rows, err := instance.Traffic()
	if err != nil || len(rows) != 1 || rows[0].UID != 301 || rows[0].Upload == 0 || rows[0].Download == 0 {
		t.Fatalf("AmneziaWG peer metering failed: %+v %v", rows, err)
	}
	peers, err := instance.(PeerObserver).PeerStatus()
	if err != nil || len(peers) != 1 || peers[0].UID != 301 || peers[0].LastHandshake == 0 {
		t.Fatal("AmneziaWG authenticated handshake status missing")
	}
	if instance.(ProcessObserver).ProcessStatus().PID <= 0 {
		t.Fatal("backend PID missing")
	}
	clientFile := filepath.Join(root, "client.key")
	WritePrivate(clientFile, []byte(clientPrivate))
	export, err := Export(s, 301, clientFile)
	if err != nil || !strings.Contains(string(export), "H1 = 123451") {
		t.Fatal("AmneziaWG export lost obfuscation settings")
	}
	next := s
	nextWG := *s.WireGuard
	next.WireGuard = &nextWG
	nextWG.Peers = nil
	if err = instance.(LiveUpdater).Update(next); err != nil {
		t.Fatal(err)
	}
	if err = exec.Command("ip", "netns", "exec", ns, "python3", "-c", program).Run(); err == nil {
		t.Fatal("revoked AmneziaWG peer forwarded traffic")
	}
	if err = instance.Close(); err != nil {
		t.Fatal(err)
	}
	if instance.(ProcessObserver).ProcessStatus().ExitCode == nil {
		t.Fatal("backend exit code missing")
	}
	if _, err = net.InterfaceByName(b.Interface); err == nil {
		t.Fatal("AmneziaWG interface survived shutdown")
	}
}

func fakeHello(secret []byte, hostname string) ([]byte, []byte) {
	session := make([]byte, 32)
	rand.Read(session)
	sni := binary.BigEndian.AppendUint16(nil, uint16(3+len(hostname)))
	sni = append(sni, 0)
	sni = binary.BigEndian.AppendUint16(sni, uint16(len(hostname)))
	sni = append(sni, []byte(hostname)...)
	ext := []byte{0, 0}
	ext = binary.BigEndian.AppendUint16(ext, uint16(len(sni)))
	ext = append(ext, sni...)
	body := []byte{3, 3}
	body = append(body, make([]byte, 32)...)
	body = append(body, 32)
	body = append(body, session...)
	body = append(body, 0, 2, 0x13, 1, 1, 0)
	body = binary.BigEndian.AppendUint16(body, uint16(len(ext)))
	body = append(body, ext...)
	handshake := []byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	handshake = append(handshake, body...)
	packet := []byte{22, 3, 1}
	packet = binary.BigEndian.AppendUint16(packet, uint16(len(handshake)))
	packet = append(packet, handshake...)
	mac := hmac.New(sha256.New, secret)
	mac.Write(packet)
	random := mac.Sum(nil)
	stamp := binary.LittleEndian.AppendUint32(nil, uint32(time.Now().Unix()))
	for i := 0; i < 4; i++ {
		random[28+i] ^= stamp[i]
	}
	copy(packet[11:43], random)
	return packet, random
}
func TestRealMTProtoBackend(t *testing.T) {
	b := realBackend(t, "V2BX_MTG_BINARY", "mtg-2.1.7")
	root := t.TempDir()
	secret := make([]byte, 16)
	rand.Read(secret)
	hostname := "example.invalid"
	b.SecretFile = filepath.Join(root, "secret")
	WritePrivate(b.SecretFile, []byte("ee"+hex.EncodeToString(secret)+hex.EncodeToString([]byte(hostname))))
	b.MetricsListen = "127.0.0.1:19348"
	b.Concurrency = 128
	b.PublicIP = "192.0.2.10"
	s := conf.LocalService{ID: "real-mtg", Type: "mtproto", Enabled: true, Listen: "127.0.0.1:19347", Backend: b}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	instance, err := Open(s, root)
	if err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		output, _ := exec.CommandContext(ctx, b.Executable, "run", filepath.Join(root, s.ID+".mtg.toml")).CombinedOutput()
		encoded := "ee" + hex.EncodeToString(secret) + hex.EncodeToString([]byte(hostname))
		t.Fatalf("MTProto startup: %v; sanitized backend output: %s", err, strings.ReplaceAll(string(output), encoded, "[redacted]"))
	}
	defer instance.Close()
	if err = instance.Health(); err != nil {
		t.Fatal(err)
	}
	c, err := net.DialTimeout("tcp", s.Listen, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(4 * time.Second))
	packet, clientRandom := fakeHello(secret, hostname)
	if _, err = c.Write(packet); err != nil {
		t.Fatal(err)
	}
	var welcome []byte
	for i := 0; i < 3; i++ {
		header := make([]byte, 5)
		if _, err = io.ReadFull(c, header); err != nil {
			t.Fatal("FakeTLS welcome was not returned:", err)
		}
		length := binary.BigEndian.Uint16(header[3:])
		body := make([]byte, length)
		if _, err = io.ReadFull(c, body); err != nil {
			t.Fatal(err)
		}
		welcome = append(welcome, header...)
		welcome = append(welcome, body...)
	}
	if len(welcome) < 43 {
		t.Fatal("invalid FakeTLS welcome")
	}
	serverRandom := append([]byte{}, welcome[11:43]...)
	clear(welcome[11:43])
	mac := hmac.New(sha256.New, secret)
	mac.Write(clientRandom)
	mac.Write(welcome)
	if !hmac.Equal(serverRandom, mac.Sum(nil)) {
		t.Fatal("FakeTLS backend did not authenticate using the configured secret")
	}
	if _, err = instance.Traffic(); err != nil {
		t.Fatal("real Prometheus collection failed:", err)
	}
	link, err := Export(s, 0, "")
	if err != nil || !strings.HasPrefix(string(link), "tg://proxy?") {
		t.Fatal("Telegram export failed")
	}
	if err = instance.Close(); err != nil {
		t.Fatal(err)
	}
	if c, err := net.DialTimeout("tcp", s.Listen, time.Second); err == nil {
		c.Close()
		t.Fatal("MTProto listener survived shutdown")
	}
}
