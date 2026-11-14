package wireguard

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/sagernet/wireguard-go/device"
)

type PeerStatistics struct {
	PublicKey     string
	Endpoint      string
	Received      int64
	Sent          int64
	LastHandshake int64
}

// ApplyPeers retains unmodified Peer objects and updates only this device.
// The input is fully validated before any UAPI mutation.
func (e *Endpoint) ApplyPeers(peers []PeerOptions) error {
	e.stateAccess.Lock()
	defer e.stateAccess.Unlock()
	d := e.device.Load()
	if d == nil {
		return fmt.Errorf("WireGuard device is not running")
	}
	options := e.options
	options.Peers = peers
	candidate, err := NewEndpoint(options)
	if err != nil {
		return fmt.Errorf("invalid WireGuard peer parameters")
	}
	var lines strings.Builder
	next := map[device.NoisePublicKey]bool{}
	for _, p := range candidate.peers {
		next[p.publicKey] = true
	}
	for _, p := range e.peers {
		if !next[p.publicKey] {
			lines.WriteString("public_key=" + hex.EncodeToString(p.publicKey[:]) + "\nremove=true\n")
		}
	}
	for _, p := range candidate.peers {
		unchanged := false
		for _, old := range e.peers {
			if reflect.DeepEqual(old, p) {
				unchanged = true
				break
			}
		}
		if unchanged {
			continue
		}
		lines.WriteString("public_key=" + hex.EncodeToString(p.publicKey[:]) + "\nreplace_allowed_ips=true\n")
		psk := p.preSharedKeyHex
		if psk == "" {
			psk = strings.Repeat("0", 64)
		}
		lines.WriteString("preshared_key=" + psk + "\npersistent_keepalive_interval=" + strconv.Itoa(int(p.keepalive)) + "\n")
		if p.endpoint.IsValid() {
			lines.WriteString("endpoint=" + p.endpoint.String() + "\n")
		}
		for _, a := range p.allowedIPs {
			lines.WriteString("allowed_ip=" + a.String() + "\n")
		}
	}
	if err = d.IpcSet(lines.String()); err != nil {
		return fmt.Errorf("WireGuard peer update rejected")
	}
	var active []*device.Peer
	for _, p := range candidate.peers {
		if peer, ok := d.LookupActivePeer(p.publicKey); ok {
			active = append(active, peer)
		}
	}
	e.tunDevice.SetDevice(d, active)
	e.peers = candidate.peers
	return nil
}

// PeerStats never exposes the device's private key or pre-shared keys.
func (e *Endpoint) PeerStats() ([]PeerStatistics, error) {
	e.stateAccess.Lock()
	defer e.stateAccess.Unlock()
	d := e.device.Load()
	if d == nil {
		return nil, fmt.Errorf("WireGuard device is not running")
	}
	text, err := d.IpcGet()
	if err != nil {
		return nil, fmt.Errorf("WireGuard statistics unavailable")
	}
	var result []PeerStatistics
	var current *PeerStatistics
	for _, line := range strings.Split(text, "\n") {
		key, value, _ := strings.Cut(line, "=")
		if key == "public_key" {
			b, err := hex.DecodeString(value)
			if err != nil {
				continue
			}
			result = append(result, PeerStatistics{PublicKey: base64.StdEncoding.EncodeToString(b)})
			current = &result[len(result)-1]
			continue
		}
		if current == nil {
			continue
		}
		switch key {
		case "endpoint":
			current.Endpoint = value
		case "rx_bytes":
			current.Received, _ = strconv.ParseInt(value, 10, 64)
		case "tx_bytes":
			current.Sent, _ = strconv.ParseInt(value, 10, 64)
		case "last_handshake_time_sec":
			current.LastHandshake, _ = strconv.ParseInt(value, 10, 64)
		}
	}
	return result, nil
}
