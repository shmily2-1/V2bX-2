package localservice

import (
	"crypto/ecdh"
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/shmily2-1/V2bX-2/conf"
)

func Export(s conf.LocalService, uid int, clientPrivateKeyFile string) ([]byte, error) {
	if s.Type == "mtproto" {
		secret, err := ReadSecret(s.Backend.SecretFile)
		if err != nil {
			return nil, err
		}
		_, port, _ := net.SplitHostPort(s.Listen)
		q := url.Values{"server": {s.Backend.PublicIP}, "port": {port}, "secret": {secret}}
		return []byte("tg://proxy?" + q.Encode() + "\n"), nil
	}
	if s.WireGuard == nil {
		return nil, fmt.Errorf("this service has no client export")
	}
	if s.WireGuard.Endpoint == "" {
		return nil, fmt.Errorf("WireGuard Endpoint is required for export")
	}
	if _, _, err := net.SplitHostPort(s.WireGuard.Endpoint); err != nil {
		return nil, fmt.Errorf("invalid WireGuard Endpoint")
	}
	var peer *conf.WireGuardPeer
	for _, p := range s.WireGuard.Peers {
		if p.UID == uid {
			v := p
			peer = &v
			break
		}
	}
	if peer == nil || len(peer.Address) == 0 {
		return nil, fmt.Errorf("peer not found or address not allocated")
	}
	private, err := ReadSecret(clientPrivateKeyFile)
	if err != nil {
		return nil, err
	}
	derive := func(text string) (string, error) {
		b, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return "", fmt.Errorf("invalid private key")
		}
		key, err := ecdh.X25519().NewPrivateKey(b)
		if err != nil {
			return "", fmt.Errorf("invalid private key")
		}
		return base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()), nil
	}
	public, err := derive(private)
	if err != nil {
		return nil, err
	}
	if public != peer.PublicKey {
		return nil, fmt.Errorf("client private key does not match peer public key")
	}
	serverKey, err := ReadSecret(s.WireGuard.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	serverPublic, err := derive(serverKey)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	out.WriteString("[Interface]\nPrivateKey = " + private + "\nAddress = " + strings.Join(peer.Address, ", ") + "\n")
	mtu := s.WireGuard.MTU
	if mtu == 0 {
		mtu = 1408
		if s.Type == "amneziawg" {
			mtu = 1420
		}
	}
	out.WriteString("MTU = " + strconv.Itoa(int(mtu)) + "\n")
	if len(s.WireGuard.DNS) > 0 {
		out.WriteString("DNS = " + strings.Join(s.WireGuard.DNS, ", ") + "\n")
	}
	if s.Type == "amneziawg" {
		for _, key := range []string{"jc", "jmin", "jmax", "s1", "s2", "s3", "s4", "h1", "h2", "h3", "h4", "i1", "i2", "i3", "i4", "i5"} {
			if v := s.Backend.Obfuscation[key]; v != "" {
				name := strings.ToUpper(key[:1]) + key[1:]
				out.WriteString(name + " = " + v + "\n")
			}
		}
	}
	out.WriteString("\n[Peer]\nPublicKey = " + serverPublic + "\nEndpoint = " + s.WireGuard.Endpoint + "\n")
	if peer.PreSharedKey != "" {
		out.WriteString("PresharedKey = " + peer.PreSharedKey + "\n")
	}
	allowed := s.WireGuard.ClientAllowedIPs
	if len(allowed) == 0 {
		for _, text := range s.WireGuard.Address {
			p, _ := netip.ParsePrefix(text)
			if p.Addr().Is4() {
				allowed = append(allowed, "0.0.0.0/0")
			} else {
				allowed = append(allowed, "::/0")
			}
		}
	}
	out.WriteString("AllowedIPs = " + strings.Join(allowed, ", ") + "\n")
	if peer.KeepAlive != 0 {
		out.WriteString("PersistentKeepalive = " + strconv.Itoa(int(peer.KeepAlive)) + "\n")
	}
	return []byte(out.String()), nil
}
