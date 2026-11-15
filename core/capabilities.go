package core

import (
	"fmt"
	"time"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
)

// ValidateNode describes the combinations implemented by V2bX's adapters,
// rather than every protocol that the underlying libraries can parse.
func ValidateNode(coreType string, info *panel.NodeInfo, options *conf.Options) error {
	if info == nil || info.Common == nil || options == nil {
		return fmt.Errorf("missing node information or options")
	}
	if err := options.CertConfig.Validate(); err != nil {
		return err
	}
	if cert := options.CertConfig; cert != nil && cert.ECHKeyFile != "" && (info.Security != panel.Tls || cert.CertMode == "" || cert.CertMode == "none") {
		return fmt.Errorf("ECH requires an enabled TLS certificate")
	}
	var network string
	switch info.Type {
	case "vmess", "vless":
		if info.VAllss == nil {
			return fmt.Errorf("missing %s parameters", info.Type)
		}
		network = info.VAllss.Network
	case "trojan":
		if info.Trojan == nil {
			return fmt.Errorf("missing trojan parameters")
		}
		network = info.Trojan.Network
	}
	if network == "" || network == "raw" {
		network = "tcp"
	}
	if info.Security == panel.Reality && info.VAllss == nil {
		return fmt.Errorf("REALITY requires vmess/vless parameters")
	}
	if info.Security == panel.Reality {
		if text := info.VAllss.RealityConfig.MaxTimeDiff; text != "" {
			duration, err := time.ParseDuration(text)
			if err != nil || duration < 0 {
				return fmt.Errorf("invalid REALITY MaxTimeDiff")
			}
		}
		names, err := info.VAllss.TlsSettings.EffectiveServerNames()
		if err != nil {
			return err
		}
		if _, err := info.VAllss.TlsSettings.EffectiveShortIDs(); err != nil {
			return err
		}
		if coreType == "sing" && len(names) > 1 {
			return fmt.Errorf("multiple REALITY server_names require xray")
		}
	}
	switch coreType {
	case "xray":
		if info.Type == "hysteria2" {
			if info.Hysteria2 == nil {
				return fmt.Errorf("missing hysteria2 parameters")
			}
			if info.Security != panel.Tls {
				return fmt.Errorf("xray hysteria2 requires TLS")
			}
			if info.Hysteria2.ObfsMode != "" && info.Hysteria2.ObfsMode != "salamander" && info.Hysteria2.ObfsMode != "gecko" {
				return fmt.Errorf("xray hysteria2 obfs mode %q is not supported", info.Hysteria2.ObfsMode)
			}
			if info.Hysteria2.ObfsType != "" && info.Hysteria2.ObfsType != "salamander" && info.Hysteria2.ObfsType != "gecko" {
				return fmt.Errorf("xray hysteria2 obfs type %q is not supported", info.Hysteria2.ObfsType)
			}
			if info.Hysteria2.ObfsMinPacketSize < 0 || info.Hysteria2.ObfsMaxPacketSize < 0 || (info.Hysteria2.ObfsMaxPacketSize > 0 && info.Hysteria2.ObfsMinPacketSize > info.Hysteria2.ObfsMaxPacketSize) {
				return fmt.Errorf("invalid hysteria2 Gecko packet size range")
			}
			if info.Hysteria2.QUICInitialPacketSize != 0 {
				return fmt.Errorf("xray hysteria2 does not expose sing-box initial_packet_size; use Core=sing")
			}
			network = "hysteria"
		}
		if info.Type == "masque" {
			if options.LimitConfig.IPLimit != 0 {
				return fmt.Errorf("MASQUE device limits are not available in this adapter")
			}
			if info.Masque == nil || len(info.Masque.Address) == 0 {
				return fmt.Errorf("MASQUE requires address pools")
			}
			if info.Security != panel.Tls || options.CertConfig == nil || options.CertConfig.CertMode == "none" || options.CertConfig.CertMode == "" {
				return fmt.Errorf("MASQUE requires TLS certificates")
			}
			for _, alpn := range options.CertConfig.ALPN {
				if alpn != "h2" && alpn != "h3" {
					return fmt.Errorf("MASQUE ALPN must be h2/h3")
				}
			}
			network = "masque"
		}
		if !isSupported(info.Type, []string{"vmess", "vless", "trojan", "shadowsocks", "hysteria2", "masque"}) {
			return fmt.Errorf("xray does not support node type %s", info.Type)
		}
		if !isSupported(network, []string{"tcp", "ws", "grpc", "httpupgrade", "splithttp", "xhttp", "kcp", "mkcp", "xdrive", "hysteria", "masque"}) {
			return fmt.Errorf("xray transport %q is not adapted", network)
		}
		if network == "xdrive" && (info.Type != "vless" || info.Security != panel.None) {
			return fmt.Errorf("XDRIVE requires VLESS without TLS/REALITY")
		}
		if network == "xdrive" && (info.VAllss.Flow != "" || info.VAllss.Encryption == "" || info.VAllss.Encryption == "none") {
			return fmt.Errorf("XDRIVE requires VLESS encryption and an empty flow")
		}
		if network == "masque" && info.Type != "masque" {
			return fmt.Errorf("MASQUE transport requires a MASQUE node")
		}
		if (network == "kcp" || network == "mkcp") && info.Security != panel.None {
			return fmt.Errorf("KCP does not support TLS/REALITY")
		}
	case "sing":
		if options.CertConfig != nil && options.CertConfig.RejectUnknownSni {
			return fmt.Errorf("RejectUnknownSni is not adapted by sing; use xray")
		}
		if (info.Type == "http" || info.Type == "mixed") && (info.HTTP == nil || (info.Security != panel.None && info.Security != panel.Tls)) {
			return fmt.Errorf("HTTP/Mixed requires valid HTTP settings and optional TLS")
		}
		if (info.Type == "http" || info.Type == "mixed") && info.Security == panel.Tls && (options.CertConfig == nil || options.CertConfig.CertMode == "none" || options.CertConfig.CertMode == "") {
			return fmt.Errorf("HTTP/Mixed TLS requires enabled certificates")
		}
		if !isSupported(info.Type, []string{"vmess", "vless", "trojan", "shadowsocks", "tuic", "anytls", "hysteria", "hysteria2", "http", "mixed"}) {
			return fmt.Errorf("sing does not support node type %s", info.Type)
		}
		if !isSupported(network, []string{"tcp", "ws", "grpc", "httpupgrade"}) {
			return fmt.Errorf("sing transport %q is not adapted; select xray if supported", network)
		}
		if info.VAllss != nil && info.VAllss.Encryption != "" && info.VAllss.Encryption != "none" {
			return fmt.Errorf("VLESS encryption requires the xray adapter")
		}
	case "hysteria2":
		if info.Type != "hysteria2" {
			return fmt.Errorf("hysteria2 core only supports hysteria2 nodes")
		}
		if options.LimitConfig.EnableDynamicSpeedLimit {
			return fmt.Errorf("standalone hysteria2 dynamic speed limiting is not adapted; use sing")
		}
		if cert := options.CertConfig; cert != nil && (len(cert.ALPN) > 0 || cert.MinVersion != "" || cert.MaxVersion != "" || len(cert.CipherSuites) > 0 || cert.ECHKeyFile != "") {
			return fmt.Errorf("standalone hysteria2 TLS tuning uses Hysteria2ConfigPath, not CertConfig TLS options")
		}
	default:
		return fmt.Errorf("unknown core type %q", coreType)
	}
	return nil
}
