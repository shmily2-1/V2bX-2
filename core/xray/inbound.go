package xray

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"encoding/json"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
	vCore "github.com/shmily2-1/V2bX-2/core"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	coreConf "github.com/xtls/xray-core/infra/conf"
)

// BuildInbound build Inbound config for different protocol
func buildInbound(option *conf.Options, nodeInfo *panel.NodeInfo, tag string) (*core.InboundHandlerConfig, error) {
	if err := vCore.ValidateNode("xray", nodeInfo, option); err != nil {
		return nil, err
	}
	if option.XrayOptions == nil {
		option.XrayOptions = conf.NewXrayOptions()
		if len(option.RawOptions) > 0 {
			if err := json.Unmarshal(option.RawOptions, option.XrayOptions); err != nil {
				return nil, fmt.Errorf("decode xray options: %w", err)
			}
		}
	}
	in := &coreConf.InboundDetourConfig{}
	var err error
	var network string
	switch nodeInfo.Type {
	case "masque":
		in.Protocol, network = "masque", "masque"
		settings, marshalErr := json.Marshal(&coreConf.MasqueServerConfig{Address: nodeInfo.Masque.Address, MTU: nodeInfo.Masque.MTU})
		if marshalErr != nil {
			return nil, marshalErr
		}
		in.Settings = (*json.RawMessage)(&settings)
		in.StreamSetting = &coreConf.StreamConfig{Network: ptrTransport("masque"), MASQUESettings: &coreConf.MasqueConfig{Host: nodeInfo.Masque.Host, Path: nodeInfo.Masque.Path}}
	case "vmess", "vless":
		err = buildV2ray(option, nodeInfo, in)
		network = nodeInfo.VAllss.Network
	case "trojan":
		err = buildTrojan(option, nodeInfo, in)
		if nodeInfo.Trojan.Network != "" {
			network = nodeInfo.Trojan.Network
		} else {
			network = "tcp"
		}
	case "shadowsocks":
		err = buildShadowsocks(option, nodeInfo, in)
		network = "tcp"
	default:
		return nil, fmt.Errorf("unsupported node type: %s, Only support: V2ray, Trojan, Shadowsocks", nodeInfo.Type)
	}
	if err != nil {
		return nil, err
	}
	network = normalizeNetwork(network)
	if in.StreamSetting == nil {
		t := coreConf.TransportProtocol(network)
		in.StreamSetting = &coreConf.StreamConfig{Network: &t}
	}
	// Set network protocol
	// Set server port
	in.PortList = &coreConf.PortList{
		Range: []coreConf.PortRange{
			{
				From: uint32(nodeInfo.Common.ServerPort),
				To:   uint32(nodeInfo.Common.ServerPort),
			}},
	}
	// Set Listen IP address
	ipAddress := net.ParseAddress(option.ListenIP)
	in.ListenOn = &coreConf.Address{Address: ipAddress}
	// Set SniffingConfig
	sniffingConfig := &coreConf.SniffingConfig{
		Enabled:      true,
		DestOverride: coreConf.StringList{"http", "tls"},
	}
	if option.XrayOptions.DisableSniffing {
		sniffingConfig.Enabled = false
	}
	in.SniffingConfig = sniffingConfig
	switch network {
	case "tcp":
		if in.StreamSetting.TCPSettings != nil {
			in.StreamSetting.TCPSettings.AcceptProxyProtocol = option.XrayOptions.EnableProxyProtocol
		} else {
			tcpSetting := &coreConf.TCPConfig{
				AcceptProxyProtocol: option.XrayOptions.EnableProxyProtocol,
			} //Enable proxy protocol
			in.StreamSetting.TCPSettings = tcpSetting
		}
	case "ws":
		if in.StreamSetting.WSSettings != nil {
			in.StreamSetting.WSSettings.AcceptProxyProtocol = option.XrayOptions.EnableProxyProtocol
		} else {
			in.StreamSetting.WSSettings = &coreConf.WebSocketConfig{
				AcceptProxyProtocol: option.XrayOptions.EnableProxyProtocol,
			} //Enable proxy protocol
		}
	default:
		socketConfig := &coreConf.SocketConfig{
			AcceptProxyProtocol: option.XrayOptions.EnableProxyProtocol,
			TFO:                 option.XrayOptions.EnableTFO,
		} //Enable proxy protocol
		in.StreamSetting.SocketSettings = socketConfig
	}
	// Set TLS or Reality settings
	switch nodeInfo.Security {
	case panel.Tls:
		// Normal tls
		if option.CertConfig == nil {
			return nil, errors.New("the CertConfig is not vail")
		}
		switch option.CertConfig.CertMode {
		case "none", "":
			break // disable
		default:
			in.StreamSetting.Security = "tls"
			in.StreamSetting.TLSSettings = &coreConf.TLSConfig{
				Certs: []*coreConf.TLSCertConfig{
					{
						CertFile:     option.CertConfig.CertFile,
						KeyFile:      option.CertConfig.KeyFile,
						OcspStapling: 3600,
					},
				},
				RejectUnknownSNI: option.CertConfig.RejectUnknownSni,
				MinVersion:       option.CertConfig.MinVersion,
				MaxVersion:       option.CertConfig.MaxVersion,
				CipherSuites:     strings.Join(option.CertConfig.CipherSuites, ":"),
			}
			if len(option.CertConfig.ALPN) > 0 {
				alpn := coreConf.StringList(option.CertConfig.ALPN)
				in.StreamSetting.TLSSettings.ALPN = &alpn
			}
			if keys, err := option.CertConfig.ECHKeys(); err != nil {
				return nil, err
			} else if len(keys) > 0 {
				in.StreamSetting.TLSSettings.ECHServerKeys = base64.StdEncoding.EncodeToString(keys)
			}
		}
	case panel.Reality:
		// Reality
		in.StreamSetting.Security = "reality"
		v := nodeInfo.VAllss
		names, err := v.TlsSettings.EffectiveServerNames()
		if err != nil {
			return nil, err
		}
		shortIDs, err := v.TlsSettings.EffectiveShortIDs()
		if err != nil {
			return nil, err
		}
		dest := v.TlsSettings.Dest
		if dest == "" {
			dest = names[0]
		}
		xver := v.TlsSettings.Xver
		if xver == 0 {
			xver = v.RealityConfig.Xver
		}
		d, err := json.Marshal(fmt.Sprintf(
			"%s:%s",
			dest,
			v.TlsSettings.ServerPort))
		if err != nil {
			return nil, fmt.Errorf("marshal reality dest error: %s", err)
		}
		mtd, _ := time.ParseDuration(v.RealityConfig.MaxTimeDiff)
		in.StreamSetting.REALITYSettings = &coreConf.REALITYConfig{
			Dest:         d,
			Xver:         xver,
			Show:         false,
			ServerNames:  names,
			PrivateKey:   v.TlsSettings.PrivateKey,
			MinClientVer: v.RealityConfig.MinClientVer,
			MaxClientVer: v.RealityConfig.MaxClientVer,
			MaxTimeDiff:  uint64(mtd.Milliseconds()),
			ShortIds:     shortIDs,
			Mldsa65Seed:  v.TlsSettings.Mldsa65Seed,
		}
	default:
		break
	}
	in.Tag = tag
	return in.Build()
}

func buildV2ray(config *conf.Options, nodeInfo *panel.NodeInfo, inbound *coreConf.InboundDetourConfig) error {
	v := nodeInfo.VAllss
	if nodeInfo.Type == "vless" {
		//Set vless
		inbound.Protocol = "vless"
		if config.XrayOptions.EnableFallback {
			if v.Encryption != "" && v.Encryption != "none" {
				return fmt.Errorf("VLESS encryption with Fallback is not adapted; refusing to disable encryption")
			}
			// Set fallback
			fallbackConfigs, err := buildVlessFallbacks(config.XrayOptions.FallBackConfigs)
			if err != nil {
				return err
			}
			s, err := json.Marshal(&coreConf.VLessInboundConfig{
				Decryption: "none",
				Fallbacks:  fallbackConfigs,
			})
			if err != nil {
				return fmt.Errorf("marshal vless fallback config error: %s", err)
			}
			inbound.Settings = (*json.RawMessage)(&s)
		} else {
			var err error
			decryption := "none"
			if nodeInfo.VAllss.Encryption != "" && nodeInfo.VAllss.Encryption != "none" {
				switch nodeInfo.VAllss.Encryption {
				case "mlkem768x25519plus":
					encSettings := nodeInfo.VAllss.EncryptionSettings
					parts := []string{
						"mlkem768x25519plus",
						encSettings.Mode,
						encSettings.Ticket,
					}
					if encSettings.ServerPadding != "" {
						parts = append(parts, encSettings.ServerPadding)
					}
					parts = append(parts, encSettings.PrivateKey)
					decryption = strings.Join(parts, ".")
				default:
					return fmt.Errorf("vless decryption method %s is not support", nodeInfo.VAllss.Encryption)
				}
			}
			s, err := json.Marshal(&coreConf.VLessInboundConfig{
				Decryption: decryption,
			})
			if err != nil {
				return fmt.Errorf("marshal vless config error: %s", err)
			}
			inbound.Settings = (*json.RawMessage)(&s)
		}
	} else {
		// Set vmess
		inbound.Protocol = "vmess"
		var err error
		s, err := json.Marshal(&coreConf.VMessInboundConfig{})
		if err != nil {
			return fmt.Errorf("marshal vmess settings error: %s", err)
		}
		inbound.Settings = (*json.RawMessage)(&s)
	}
	stream, err := buildStream(v.Network, v.NetworkSettings)
	inbound.StreamSetting = stream
	return err
}

func buildTrojan(config *conf.Options, nodeInfo *panel.NodeInfo, inbound *coreConf.InboundDetourConfig) error {
	inbound.Protocol = "trojan"
	v := nodeInfo.Trojan
	if config.XrayOptions.EnableFallback {
		// Set fallback
		fallbackConfigs, err := buildTrojanFallbacks(config.XrayOptions.FallBackConfigs)
		if err != nil {
			return err
		}
		s, err := json.Marshal(&coreConf.TrojanServerConfig{
			Fallbacks: fallbackConfigs,
		})
		inbound.Settings = (*json.RawMessage)(&s)
		if err != nil {
			return fmt.Errorf("marshal trojan fallback config error: %s", err)
		}
	} else {
		s := []byte("{}")
		inbound.Settings = (*json.RawMessage)(&s)
	}
	stream, err := buildStream(v.Network, v.NetworkSettings)
	inbound.StreamSetting = stream
	return err
}

func normalizeNetwork(network string) string {
	switch network {
	case "", "raw":
		return "tcp"
	case "mkcp":
		return "kcp"
	default:
		return network
	}
}

func buildStream(network string, settings json.RawMessage) (*coreConf.StreamConfig, error) {
	network = normalizeNetwork(network)
	t := coreConf.TransportProtocol(network)
	stream := &coreConf.StreamConfig{Network: &t}
	var target any
	switch network {
	case "tcp":
		target = &stream.TCPSettings
	case "ws":
		target = &stream.WSSettings
	case "grpc":
		target = &stream.GRPCSettings
	case "httpupgrade":
		target = &stream.HTTPUPGRADESettings
	case "splithttp", "xhttp":
		target = &stream.SplitHTTPSettings
	case "kcp":
		target = &stream.KCPSettings
	case "xdrive":
		target = &stream.XDRIVESettings
	default:
		return nil, fmt.Errorf("xray transport %q is not adapted", network)
	}
	if len(settings) > 0 {
		if network == "kcp" {
			var legacy struct {
				Seed   string `json:"seed"`
				Header struct {
					Type string `json:"type"`
				} `json:"header"`
				Congestion      *bool `json:"congestion"`
				ReadBufferSize  *int  `json:"readBufferSize"`
				WriteBufferSize *int  `json:"writeBufferSize"`
			}
			if err := json.Unmarshal(settings, &legacy); err != nil {
				return nil, fmt.Errorf("decode KCP settings: %w", err)
			}
			if legacy.Seed != "" || (legacy.Header.Type != "" && legacy.Header.Type != "none") || legacy.Congestion != nil || legacy.ReadBufferSize != nil || legacy.WriteBufferSize != nil {
				return nil, fmt.Errorf("this Xray version no longer applies legacy KCP seed/header/congestion/buffer settings")
			}
		}
		if err := json.Unmarshal(settings, target); err != nil {
			return nil, fmt.Errorf("decode %s settings: %w", network, err)
		}
	}
	return stream, nil
}

func ptrTransport(network string) *coreConf.TransportProtocol {
	value := coreConf.TransportProtocol(network)
	return &value
}

func buildShadowsocks(config *conf.Options, nodeInfo *panel.NodeInfo, inbound *coreConf.InboundDetourConfig) error {
	inbound.Protocol = "shadowsocks"
	s := nodeInfo.Shadowsocks
	settings := &coreConf.ShadowsocksServerConfig{
		Cipher: s.Cipher,
	}
	p := make([]byte, 32)
	_, err := rand.Read(p)
	if err != nil {
		return fmt.Errorf("generate random password error: %s", err)
	}
	randomPasswd := hex.EncodeToString(p)
	cipher := s.Cipher
	if s.ServerKey != "" {
		settings.Password = s.ServerKey
		randomPasswd = base64.StdEncoding.EncodeToString([]byte(randomPasswd))
		cipher = ""
	}
	defaultSSuser := &coreConf.ShadowsocksUserConfig{
		Cipher:   cipher,
		Password: randomPasswd,
	}
	settings.Users = append(settings.Users, defaultSSuser)
	settings.NetworkList = &coreConf.NetworkList{"tcp", "udp"}
	// Xray 26.3.27 removed the optional IVCheck switch. Replay protection
	// is controlled by the selected protocol implementation, not this flag.
	t := coreConf.TransportProtocol("tcp")
	inbound.StreamSetting = &coreConf.StreamConfig{Network: &t}
	sets, err := json.Marshal(settings)
	inbound.Settings = (*json.RawMessage)(&sets)
	if err != nil {
		return fmt.Errorf("marshal shadowsocks settings error: %s", err)
	}
	return nil
}

func buildVlessFallbacks(fallbackConfigs []conf.FallBackConfigForXray) ([]*coreConf.VLessInboundFallback, error) {
	if fallbackConfigs == nil {
		return nil, fmt.Errorf("you must provide FallBackConfigs")
	}
	vlessFallBacks := make([]*coreConf.VLessInboundFallback, len(fallbackConfigs))
	for i, c := range fallbackConfigs {
		if c.Dest == "" {
			return nil, fmt.Errorf("dest is required for fallback fialed")
		}
		var dest json.RawMessage
		dest, err := json.Marshal(c.Dest)
		if err != nil {
			return nil, fmt.Errorf("marshal dest %s config fialed: %s", dest, err)
		}
		vlessFallBacks[i] = &coreConf.VLessInboundFallback{
			Name: c.SNI,
			Alpn: c.Alpn,
			Path: c.Path,
			Dest: dest,
			Xver: c.ProxyProtocolVer,
		}
	}
	return vlessFallBacks, nil
}

func buildTrojanFallbacks(fallbackConfigs []conf.FallBackConfigForXray) ([]*coreConf.TrojanInboundFallback, error) {
	if fallbackConfigs == nil {
		return nil, fmt.Errorf("you must provide FallBackConfigs")
	}

	trojanFallBacks := make([]*coreConf.TrojanInboundFallback, len(fallbackConfigs))
	for i, c := range fallbackConfigs {

		if c.Dest == "" {
			return nil, fmt.Errorf("dest is required for fallback fialed")
		}

		var dest json.RawMessage
		dest, err := json.Marshal(c.Dest)
		if err != nil {
			return nil, fmt.Errorf("marshal dest %s config fialed: %s", dest, err)
		}
		trojanFallBacks[i] = &coreConf.TrojanInboundFallback{
			Name: c.SNI,
			Alpn: c.Alpn,
			Path: c.Path,
			Dest: dest,
			Xver: c.ProxyProtocolVer,
		}
	}
	return trojanFallBacks, nil
}
