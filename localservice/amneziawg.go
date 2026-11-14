package localservice

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os/exec"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shmily2-1/V2bX-2/conf"
)

func init() { Register("amneziawg", openAmneziaWG) }

type amneziaBackend struct {
	process  *childProcess
	spec     conf.LocalService
	previous map[string][2]int64
}

func wgHex(key string) string {
	b, _ := base64.StdEncoding.DecodeString(key)
	return hex.EncodeToString(b)
}
func awgPeerLines(peers []conf.WireGuardPeer) string {
	var text strings.Builder
	for _, p := range peers {
		text.WriteString("public_key=" + wgHex(p.PublicKey) + "\nreplace_allowed_ips=true\n")
		psk := wgHex(p.PreSharedKey)
		if psk == "" {
			psk = strings.Repeat("0", 64)
		}
		text.WriteString("preshared_key=" + psk + "\npersistent_keepalive_interval=" + strconv.Itoa(int(p.KeepAlive)) + "\n")
		for _, a := range p.Address {
			text.WriteString("allowed_ip=" + a + "\n")
		}
	}
	return text.String()
}
func uapi(socket, operation, body string) (string, error) {
	c, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return "", fmt.Errorf("AmneziaWG UAPI unavailable")
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = io.WriteString(c, operation+"=1\n"+body+"\n"); err != nil {
		return "", err
	}
	reader := bufio.NewReader(io.LimitReader(c, 4<<20))
	var reply strings.Builder
	ok := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("incomplete AmneziaWG UAPI response")
		}
		if line == "\n" {
			break
		}
		if line == "errno=0\n" {
			ok = true
		}
		reply.WriteString(line)
	}
	if !ok {
		return "", fmt.Errorf("AmneziaWG rejected configuration")
	}
	return reply.String(), nil
}
func openAmneziaWG(s conf.LocalService, _ string) (Instance, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("AmneziaWG backend currently requires Linux")
	}
	if _, err := net.InterfaceByName(s.Backend.Interface); err == nil {
		return nil, fmt.Errorf("AmneziaWG interface already exists; refusing to take ownership")
	}
	key, err := ReadSecret(s.WireGuard.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	if !conf.ValidWGKey(key) {
		return nil, fmt.Errorf("invalid AmneziaWG private key")
	}
	p, err := startChild(s.Backend, "-f", s.Backend.Interface)
	if err != nil {
		return nil, err
	}
	b := &amneziaBackend{process: p, spec: s, previous: map[string][2]int64{}}
	ready := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if p.Health() != nil {
			break
		}
		if _, err = uapi(s.Backend.ControlSocket, "get", ""); err == nil {
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		_ = p.Close()
		return nil, fmt.Errorf("AmneziaWG UAPI did not become ready")
	}
	_, port, _ := net.SplitHostPort(s.Listen)
	text := "private_key=" + wgHex(key) + "\nlisten_port=" + port + "\nreplace_peers=true\n"
	for k, v := range s.Backend.Obfuscation {
		text += k + "=" + v + "\n"
	}
	text += awgPeerLines(s.WireGuard.Peers)
	if _, err = uapi(s.Backend.ControlSocket, "set", text); err != nil {
		_ = p.Close()
		return nil, err
	}
	for _, address := range s.WireGuard.Address {
		if err = exec.Command("ip", "address", "add", address, "dev", s.Backend.Interface).Run(); err != nil {
			_ = p.Close()
			return nil, fmt.Errorf("AmneziaWG interface address setup failed")
		}
	}
	mtu := s.WireGuard.MTU
	if mtu == 0 {
		mtu = 1420
	}
	if err = exec.Command("ip", "link", "set", "dev", s.Backend.Interface, "mtu", strconv.Itoa(int(mtu)), "up").Run(); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("AmneziaWG interface activation failed")
	}
	return b, nil
}
func (b *amneziaBackend) Health() error {
	if err := b.process.Health(); err != nil {
		return err
	}
	_, err := uapi(b.spec.Backend.ControlSocket, "get", "")
	return err
}
func (b *amneziaBackend) Close() error                 { return b.process.Close() }
func (b *amneziaBackend) ProcessStatus() ProcessStatus { return b.process.Status() }
func (b *amneziaBackend) PeerStatus() ([]PeerStatus, error) {
	text, err := uapi(b.spec.Backend.ControlSocket, "get", "")
	if err != nil {
		return nil, err
	}
	peers := map[string]conf.WireGuardPeer{}
	for _, p := range b.spec.WireGuard.Peers {
		peers[wgHex(p.PublicKey)] = p
	}
	var result []PeerStatus
	var current *PeerStatus
	for _, line := range strings.Split(text, "\n") {
		key, value, _ := strings.Cut(line, "=")
		if key == "public_key" {
			p, ok := peers[value]
			current = nil
			if ok {
				result = append(result, PeerStatus{UID: p.UID, PublicKey: p.PublicKey, Address: p.Address})
				current = &result[len(result)-1]
			}
			continue
		}
		if current != nil {
			switch key {
			case "endpoint":
				current.Endpoint = value
			case "last_handshake_time_sec":
				current.LastHandshake, _ = strconv.ParseInt(value, 10, 64)
			}
		}
	}
	return result, nil
}
func (b *amneziaBackend) Update(s conf.LocalService) error {
	old, next := b.spec, s
	oldWG, nextWG := *old.WireGuard, *next.WireGuard
	oldWG.Peers, nextWG.Peers = nil, nil
	old.WireGuard, next.WireGuard = &oldWG, &nextWG
	if !reflect.DeepEqual(old, next) {
		return ErrRestartRequired
	}
	newPeers := map[string]bool{}
	for _, p := range s.WireGuard.Peers {
		newPeers[p.PublicKey] = true
	}
	text := ""
	for _, p := range b.spec.WireGuard.Peers {
		if !newPeers[p.PublicKey] {
			text += "public_key=" + wgHex(p.PublicKey) + "\nremove=true\n"
		}
	}
	text += awgPeerLines(s.WireGuard.Peers)
	if _, err := uapi(s.Backend.ControlSocket, "set", text); err != nil {
		if _, restoreErr := uapi(s.Backend.ControlSocket, "set", "replace_peers=true\n"+awgPeerLines(b.spec.WireGuard.Peers)); restoreErr != nil {
			_ = b.process.Close()
			return fmt.Errorf("AmneziaWG peer update and rollback failed")
		}
		return err
	}
	for _, p := range b.spec.WireGuard.Peers {
		if !newPeers[p.PublicKey] {
			delete(b.previous, p.PublicKey)
		}
	}
	b.spec = s
	return nil
}
func (b *amneziaBackend) Traffic() ([]Traffic, error) {
	text, err := uapi(b.spec.Backend.ControlSocket, "get", "")
	if err != nil {
		return nil, err
	}
	uidMap := map[string]int{}
	for _, p := range b.spec.WireGuard.Peers {
		uidMap[wgHex(p.PublicKey)] = p.UID
	}
	current := map[string][2]int64{}
	key := ""
	for _, line := range strings.Split(text, "\n") {
		k, v, _ := strings.Cut(line, "=")
		if k == "public_key" {
			key = v
		}
		if uidMap[key] == 0 {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if k != "rx_bytes" && k != "tx_bytes" {
			continue
		}
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid AmneziaWG traffic counter")
		}
		pair := current[key]
		if k == "rx_bytes" {
			pair[0] = n
		} else {
			pair[1] = n
		}
		current[key] = pair
	}
	var result []Traffic
	for key, pair := range current {
		public, err := hex.DecodeString(key)
		if err != nil {
			continue
		}
		name := base64.StdEncoding.EncodeToString(public)
		old := b.previous[name]
		delta := [2]int64{pair[0] - old[0], pair[1] - old[1]}
		for i := range delta {
			if delta[i] < 0 {
				delta[i] = pair[i]
			}
		}
		result = append(result, Traffic{UID: uidMap[key], Upload: delta[0], Download: delta[1]})
		b.previous[name] = pair
	}
	return result, nil
}
