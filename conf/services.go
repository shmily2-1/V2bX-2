package conf

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// LocalService is independent of panel users: unauthenticated tunnels are
// service-metered, never charged to a fabricated panel UID.
type LocalService struct {
	ID        string            `json:"ID"`
	Type      string            `json:"Type"`
	Enabled   bool              `json:"Enabled"`
	Revision  uint64            `json:"Revision,omitempty"`
	Listen    string            `json:"Listen,omitempty"`
	Target    string            `json:"Target,omitempty"`
	Network   string            `json:"Network,omitempty"`
	TUN       *TUNService       `json:"TUN,omitempty"`
	WireGuard *WireGuardService `json:"WireGuard,omitempty"`
	Backend   *BackendService   `json:"Backend,omitempty"`
}
type TUNService struct {
	OutboundInterface   string   `json:"OutboundInterface,omitempty"`
	Interface           string   `json:"Interface"`
	Address             []string `json:"Address"`
	MTU                 uint32   `json:"MTU,omitempty"`
	AutoRoute           bool     `json:"AutoRoute,omitempty"`
	RouteAddress        []string `json:"RouteAddress,omitempty"`
	RouteExcludeAddress []string `json:"RouteExcludeAddress,omitempty"`
}
type WireGuardService struct {
	PrivateKeyFile   string          `json:"PrivateKeyFile"`
	Address          []string        `json:"Address"`
	MTU              uint32          `json:"MTU,omitempty"`
	Endpoint         string          `json:"Endpoint,omitempty"`
	DNS              []string        `json:"DNS,omitempty"`
	ClientAllowedIPs []string        `json:"ClientAllowedIPs,omitempty"`
	Peers            []WireGuardPeer `json:"Peers"`
}
type WireGuardPeer struct {
	UID          int      `json:"UID"`
	PublicKey    string   `json:"PublicKey"`
	PreSharedKey string   `json:"PreSharedKey,omitempty"`
	Address      []string `json:"Address,omitempty"`
	KeepAlive    uint16   `json:"KeepAlive,omitempty"`
	SpeedLimit   int      `json:"SpeedLimit,omitempty"` // Mbps; userspace WireGuard only.
}
type BackendService struct {
	Executable    string            `json:"Executable"`
	SHA256        string            `json:"SHA256"`
	Version       string            `json:"Version"`
	Interface     string            `json:"Interface,omitempty"`
	ControlSocket string            `json:"ControlSocket,omitempty"`
	Obfuscation   map[string]string `json:"Obfuscation,omitempty"`
	SecretFile    string            `json:"SecretFile,omitempty"`
	MetricsListen string            `json:"MetricsListen,omitempty"`
	PublicIP      string            `json:"PublicIP,omitempty"`
	Concurrency   int               `json:"Concurrency,omitempty"`
}

var serviceID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,47}$`)

func ValidServiceID(id string) bool { return serviceID.MatchString(id) }

func (s *LocalService) ResolvePaths(base string) {
	resolve := func(path *string) {
		if *path != "" && !filepath.IsAbs(*path) {
			*path = filepath.Join(base, *path)
		}
	}
	if s.WireGuard != nil {
		resolve(&s.WireGuard.PrivateKeyFile)
	}
	if s.Backend != nil {
		resolve(&s.Backend.Executable)
		resolve(&s.Backend.SecretFile)
	}
}

func ValidWGKey(key string) bool {
	b, err := base64.StdEncoding.DecodeString(key)
	return err == nil && len(b) == 32 && key != base64.StdEncoding.EncodeToString(make([]byte, 32))
}
func (s LocalService) Validate() error {
	if !ValidServiceID(s.ID) {
		return fmt.Errorf("invalid service ID")
	}
	if s.TUN != nil && s.Type != "tun" || s.WireGuard != nil && s.Type != "wireguard" && s.Type != "amneziawg" || s.Backend != nil && s.Type != "amneziawg" && s.Type != "mtproto" {
		return fmt.Errorf("service contains settings for a different backend")
	}
	switch s.Type {
	case "tunnel":
		if s.Network != "tcp" && s.Network != "udp" && s.Network != "tcp,udp" {
			return fmt.Errorf("tunnel Network must be tcp, udp or tcp,udp")
		}
		host, port, err := net.SplitHostPort(s.Target)
		n, parseErr := strconv.Atoi(port)
		if err != nil || host == "" || strings.ContainsAny(host, "\r\n\t ") || parseErr != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid tunnel Target")
		}
	case "tun":
		if s.TUN == nil || !ValidServiceID(s.TUN.Interface) || len(s.TUN.Interface) > 15 || len(s.TUN.Address) == 0 {
			return fmt.Errorf("TUN requires interface and addresses")
		}
		for _, a := range append(append(append([]string{}, s.TUN.Address...), s.TUN.RouteAddress...), s.TUN.RouteExcludeAddress...) {
			if _, err := netip.ParsePrefix(a); err != nil {
				return fmt.Errorf("invalid TUN prefix")
			}
		}
		if s.TUN.MTU != 0 && (s.TUN.MTU < 1280 || s.TUN.MTU > 65535) {
			return fmt.Errorf("invalid TUN MTU")
		}
		if s.TUN.OutboundInterface != "" && !ValidServiceID(s.TUN.OutboundInterface) {
			return fmt.Errorf("invalid TUN outbound interface")
		}
		return nil
	case "wireguard", "amneziawg":
		if s.WireGuard == nil || s.WireGuard.PrivateKeyFile == "" || len(s.WireGuard.Address) == 0 {
			return fmt.Errorf("WireGuard requires private key file and address pools")
		}
		wg := s.WireGuard
		families := map[bool]bool{}
		for _, a := range wg.Address {
			p, err := netip.ParsePrefix(a)
			if err != nil || families[p.Addr().Is4()] || p.Bits() >= p.Addr().BitLen()-1 {
				return fmt.Errorf("invalid/duplicate WireGuard pool")
			}
			families[p.Addr().Is4()] = true
		}
		if wg.MTU != 0 && (wg.MTU < 1280 || wg.MTU > 65535) {
			return fmt.Errorf("invalid WireGuard MTU")
		}
		for _, a := range wg.DNS {
			if _, err := netip.ParseAddr(a); err != nil {
				return fmt.Errorf("invalid WireGuard DNS address")
			}
		}
		for _, a := range wg.ClientAllowedIPs {
			if _, err := netip.ParsePrefix(a); err != nil {
				return fmt.Errorf("invalid client AllowedIPs")
			}
		}
		keys, uids, ips := map[string]bool{}, map[int]bool{}, map[netip.Addr]bool{}
		for _, u := range wg.Peers {
			if u.UID <= 0 || u.SpeedLimit < 0 || !ValidWGKey(u.PublicKey) || (u.PreSharedKey != "" && !ValidWGKey(u.PreSharedKey)) || keys[u.PublicKey] || uids[u.UID] {
				return fmt.Errorf("invalid/duplicate WireGuard peer")
			}
			keys[u.PublicKey], uids[u.UID] = true, true
			if s.Type == "amneziawg" && u.SpeedLimit != 0 {
				return fmt.Errorf("AmneziaWG per-peer speed limits are not implemented")
			}
			for _, text := range u.Address {
				p, err := netip.ParsePrefix(text)
				if err != nil || p.Bits() != p.Addr().BitLen() || ips[p.Addr()] {
					return fmt.Errorf("peer addresses must be unique host prefixes")
				}
				allowed := false
				for _, poolText := range wg.Address {
					pool, _ := netip.ParsePrefix(poolText)
					if pool.Contains(p.Addr()) && pool.Addr() != p.Addr() && pool.Masked().Addr() != p.Addr() && (!p.Addr().Is4() || pool.Contains(p.Addr().Next())) {
						allowed = true
					}
				}
				if !allowed {
					return fmt.Errorf("peer address outside pool or reserved")
				}
				ips[p.Addr()] = true
			}
		}
		if s.Type == "wireguard" {
			break
		}
		fallthrough
	case "mtproto":
		b := s.Backend
		if b == nil || !filepath.IsAbs(b.Executable) || b.Version == "" {
			return fmt.Errorf("external backend requires absolute Executable, Version and SHA256")
		}
		if hash, err := hex.DecodeString(b.SHA256); err != nil || len(hash) != 32 {
			return fmt.Errorf("invalid backend SHA256")
		}
		if s.Type == "mtproto" {
			if b.Version != "mtg-2.1.7" || b.SecretFile == "" || b.PublicIP == "" || b.Concurrency <= 0 || b.MetricsListen == "" {
				return fmt.Errorf("MTProto requires mtg-2.1.7, SecretFile, PublicIP, MetricsListen and positive Concurrency")
			}
			if _, err := netip.ParseAddr(b.PublicIP); err != nil {
				return fmt.Errorf("invalid MTProto public IP")
			}
			if b.MetricsListen != "" {
				host, _, err := net.SplitHostPort(b.MetricsListen)
				ip := net.ParseIP(host)
				if err != nil || ip == nil || !ip.IsLoopback() {
					return fmt.Errorf("metrics must listen on loopback")
				}
			}
		} else {
			if b.Version != "amneziawg-go-v3.1.20260828" {
				return fmt.Errorf("unsupported AmneziaWG backend version")
			}
			if !ValidServiceID(b.Interface) || len(b.Interface) > 15 || b.ControlSocket != filepath.Join("/var/run/amneziawg", b.Interface+".sock") {
				return fmt.Errorf("invalid AmneziaWG interface/socket")
			}
			allowed := map[string]bool{"jc": true, "jmin": true, "jmax": true, "s1": true, "s2": true, "s3": true, "s4": true, "h1": true, "h2": true, "h3": true, "h4": true, "i1": true, "i2": true, "i3": true, "i4": true, "i5": true}
			for k, v := range b.Obfuscation {
				if !allowed[k] || v == "" || strings.ContainsAny(v, "\r\n\x00") || len(v) > 4096 {
					return fmt.Errorf("unsupported AmneziaWG obfuscation parameter")
				}
				if strings.HasPrefix(k, "i") {
					if !regexp.MustCompile(`^(<(b 0x[0-9a-fA-F]+|r [0-9]+|rd [0-9]+|rc [0-9]+|t)>)+$`).MatchString(v) {
						return fmt.Errorf("invalid AmneziaWG signature packet")
					}
					continue
				}
				parts := strings.Split(v, "-")
				if len(parts) > 1 && !strings.HasPrefix(k, "h") || len(parts) > 2 {
					return fmt.Errorf("invalid AmneziaWG numeric value")
				}
				var first uint64
				for i, part := range parts {
					n, err := strconv.ParseUint(part, 10, 32)
					if err != nil || i == 1 && n < first {
						return fmt.Errorf("invalid AmneziaWG numeric range")
					}
					first = n
				}
			}
			if s.Listen != "" {
				host, _, _ := net.SplitHostPort(s.Listen)
				if host != "0.0.0.0" && host != "::" {
					return fmt.Errorf("AmneziaWG Listen must use a wildcard IP")
				}
			}
		}
	default:
		return fmt.Errorf("unsupported service type %q", s.Type)
	}
	host, port, err := net.SplitHostPort(s.Listen)
	if err != nil || net.ParseIP(host) == nil || port == "0" || port == "" {
		return fmt.Errorf("service Listen requires an IP and nonzero port")
	}
	if address, err := netip.ParseAddrPort(s.Listen); err != nil || address.Port() == 0 {
		return fmt.Errorf("invalid service Listen")
	}
	return nil
}
