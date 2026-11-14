//go:build sing && xray && hysteria2 && with_quic && with_wireguard

package core_test

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/localservice"
)

func wgTestKey(t *testing.T) (string, string) {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k.Bytes()), base64.StdEncoding.EncodeToString(k.PublicKey().Bytes())
}
func udpPort(t *testing.T) int {
	t.Helper()
	p, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := p.LocalAddr().(*net.UDPAddr).Port
	p.Close()
	return port
}
func TestLocalWireGuardPeerDataPlane(t *testing.T) {
	target := routableEcho(t)
	root := t.TempDir()
	serverPrivate, serverPublic := wgTestKey(t)
	private1, public1 := wgTestKey(t)
	private2, public2 := wgTestKey(t)
	keyPath := filepath.Join(root, "server.key")
	if err := os.WriteFile(keyPath, []byte(serverPrivate), 0600); err != nil {
		t.Fatal(err)
	}
	port := udpPort(t)
	spec := conf.LocalService{ID: "wg-test", Type: "wireguard", Enabled: true, Listen: fmt.Sprintf("0.0.0.0:%d", port), WireGuard: &conf.WireGuardService{PrivateKeyFile: keyPath, Address: []string{"10.195.0.1/24"}, Endpoint: fmt.Sprintf("127.0.0.1:%d", port), Peers: []conf.WireGuardPeer{{UID: 101, PublicKey: public1}, {UID: 102, PublicKey: public2}}}}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	spec, allocation, err := localservice.Allocate(spec, filepath.Join(root, "allocation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = localservice.WriteJSON(filepath.Join(root, "allocation.json"), allocation); err != nil {
		t.Fatal(err)
	}
	instance, err := localservice.Open(spec, root)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	client := func(private, address string) map[string]any {
		return map[string]any{"log": map[string]any{"loglevel": "none"}, "outbounds": []any{map[string]any{"protocol": "wireguard", "settings": map[string]any{"noKernelTun": true, "secretKey": private, "address": []string{address}, "mtu": 1280, "peers": []any{map[string]any{"publicKey": serverPublic, "endpoint": fmt.Sprintf("127.0.0.1:%d", port), "allowedIPs": []string{"0.0.0.0/0"}}}}}}}
	}
	first := extendedClient(t, client(private1, spec.WireGuard.Peers[0].Address[0]))
	second := extendedClient(t, client(private2, spec.WireGuard.Peers[1].Address[0]))
	if err = clientEcho(first, target); err != nil {
		t.Fatal("first peer:", err)
	}
	if err = clientEcho(second, target); err != nil {
		t.Fatal("second peer:", err)
	}
	traffic := waitLocalTraffic(t, instance, 101, 102)
	seen := map[int]bool{}
	for _, r := range traffic {
		if r.Upload > 0 && r.Download > 0 {
			seen[r.UID] = true
		}
	}
	if !seen[101] || !seen[102] {
		t.Fatalf("missing per-peer accounting: %+v", traffic)
	}
	clientKeyFile := filepath.Join(root, "client.key")
	os.WriteFile(clientKeyFile, []byte(private2), 0600)
	exported, err := localservice.Export(spec, 102, clientKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exported), "PublicKey = "+serverPublic) || !strings.Contains(string(exported), "PrivateKey = "+private2) || strings.Contains(string(exported), serverPrivate) {
		t.Fatal("incorrect client export")
	}
	next := spec
	nextWG := *spec.WireGuard
	next.WireGuard = &nextWG
	nextWG.Peers = append([]conf.WireGuardPeer{}, spec.WireGuard.Peers[1:]...)
	if err = instance.(localservice.LiveUpdater).Update(next); err != nil {
		t.Fatal(err)
	}
	if err = clientEcho(first, target); err == nil {
		t.Fatal("revoked peer connected")
	}
	if err = clientEcho(second, target); err != nil {
		t.Fatal("unmodified peer was interrupted:", err)
	}
	private3, public3 := wgTestKey(t)
	nextWG.Peers = append(nextWG.Peers, conf.WireGuardPeer{UID: 103, PublicKey: public3, Address: []string{"10.195.0.4/32"}})
	if err = instance.(localservice.LiveUpdater).Update(next); err != nil {
		t.Fatal(err)
	}
	third := extendedClient(t, client(private3, "10.195.0.4/32"))
	if err = clientEcho(third, target); err != nil {
		t.Fatal("new peer:", err)
	}
	if err = instance.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := localservice.Open(next, root)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	reconnected := extendedClient(t, client(private2, spec.WireGuard.Peers[1].Address[0]))
	if err = clientEcho(reconnected, target); err != nil {
		t.Fatal("peer could not connect after service restart:", err)
	}
}
func TestLocalTunnelDataPlane(t *testing.T) {
	target := p1Echo(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	spec := conf.LocalService{ID: "tunnel-test", Type: "tunnel", Listen: address, Target: target, Network: "tcp"}
	instance, err := localservice.Open(spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	if err = echoPayload(c); err != nil {
		t.Fatal(err)
	}
	c.Close()
	traffic := waitLocalTraffic(t, instance, 0)
	if len(traffic) != 1 || traffic[0].UID != 0 || traffic[0].Upload == 0 || traffic[0].Download == 0 {
		t.Fatalf("invalid service traffic: %+v", traffic)
	}
	if err = instance.Close(); err != nil {
		t.Fatal(err)
	}
	reused, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("listener not released:", err)
	}
	reused.Close()
}
func TestLocalTUNLifecycle(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("V2BX_ISOLATED_TEST") != "1" {
		t.Skip("TUN requires isolated Linux network namespace")
	}
	spec := conf.LocalService{ID: "tun-test", Type: "tun", TUN: &conf.TUNService{Interface: "v2bxtest", Address: []string{"10.199.0.1/30"}, MTU: 1400}}
	instance, err := localservice.Open(spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = instance.Health(); err != nil {
		instance.Close()
		t.Fatal(err)
	}
	if err = instance.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = net.InterfaceByName("v2bxtest"); err == nil {
		t.Fatal("TUN interface was not released")
	}
}

func waitLocalTraffic(t *testing.T, instance localservice.Instance, uids ...int) []localservice.Traffic {
	t.Helper()
	totals := map[int]localservice.Traffic{}
	for until := time.Now().Add(2 * time.Second); time.Now().Before(until); {
		rows, err := instance.Traffic()
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			sum := totals[row.UID]
			sum.UID = row.UID
			sum.Upload += row.Upload
			sum.Download += row.Download
			totals[row.UID] = sum
		}
		ready := true
		for _, uid := range uids {
			sum := totals[uid]
			if sum.Upload == 0 || sum.Download == 0 {
				ready = false
			}
		}
		if ready {
			var result []localservice.Traffic
			for _, uid := range uids {
				result = append(result, totals[uid])
			}
			return result
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("service counters did not observe both directions: %+v", totals)
	return nil
}
