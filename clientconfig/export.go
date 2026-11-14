package clientconfig

import (
	"crypto/ecdh"
	"crypto/mlkem"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
)

// Build returns an outbound configuration, never a server private key.
func Build(info *panel.NodeInfo, options *conf.Options, user panel.UserInfo, server string, allowStorageCredentials bool) ([]byte, error) {
	if info == nil || info.Common == nil {
		return nil, fmt.Errorf("missing node information")
	}
	if server == "" {
		server = info.Common.Host
	}
	if server == "" {
		return nil, fmt.Errorf("export requires --server or the panel's host")
	}
	if net.ParseIP(server) == nil && strings.ContainsAny(server, "/: \t\r\n") {
		return nil, fmt.Errorf("server must be a hostname or IP")
	}
	switch info.Type {
	case "http", "mixed":
		if user.Password == "" || user.Uuid == "" {
			return nil, fmt.Errorf("missing HTTP user credentials")
		}
		kind := "http"
		if info.Type == "mixed" {
			kind = "socks"
		}
		out := map[string]any{"protocol": kind, "tag": "proxy", "settings": map[string]any{"address": server, "port": info.Common.ServerPort, "user": user.Uuid, "pass": user.Password}}
		if info.Security == panel.Tls {
			out["streamSettings"] = map[string]any{"network": "tcp", "security": "tls", "tlsSettings": clientTLS(info, options, server)}
		}
		return json.MarshalIndent(map[string]any{"outbounds": []any{out}}, "", "  ")
	case "masque":
		if info.Masque == nil || user.Password == "" {
			return nil, fmt.Errorf("missing MASQUE parameters")
		}
		stream := map[string]any{"network": "masque", "security": "tls", "tlsSettings": clientTLS(info, options, server), "masqueSettings": map[string]any{"host": info.Masque.Host, "path": info.Masque.Path, "headers": map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(user.Uuid+":"+user.Password))}}}
		return json.MarshalIndent(map[string]any{"outbounds": []any{map[string]any{"protocol": "masque", "tag": "proxy", "settings": map[string]any{"address": server, "port": info.Common.ServerPort}, "streamSettings": stream}}}, "", "  ")
	case "vless":
		if info.VAllss == nil || info.VAllss.Network != "xdrive" {
			return nil, fmt.Errorf("this exporter supports VLESS XDRIVE, MASQUE and HTTP/Mixed")
		}
		var settings map[string]any
		if err := json.Unmarshal(info.VAllss.NetworkSettings, &settings); err != nil {
			return nil, fmt.Errorf("invalid XDRIVE settings")
		}
		if settings["service"] != "local" && !allowStorageCredentials {
			return nil, fmt.Errorf("remote XDRIVE export requires --allow-storage-credentials; give clients isolated storage credentials")
		}
		v := info.VAllss
		if v.Encryption != "mlkem768x25519plus" {
			return nil, fmt.Errorf("XDRIVE requires VLESS encryption")
		}
		keys := []string{}
		for _, part := range strings.Split(v.EncryptionSettings.PrivateKey, ".") {
			b, err := base64.RawURLEncoding.DecodeString(part)
			if err != nil {
				return nil, fmt.Errorf("invalid VLESS private key")
			}
			var public []byte
			switch len(b) {
			case 32:
				key, err := ecdh.X25519().NewPrivateKey(b)
				if err != nil {
					return nil, fmt.Errorf("invalid VLESS X25519 key")
				}
				public = key.PublicKey().Bytes()
			case 64:
				key, err := mlkem.NewDecapsulationKey768(b)
				if err != nil {
					return nil, fmt.Errorf("invalid VLESS ML-KEM seed")
				}
				public = key.EncapsulationKey().Bytes()
			default:
				return nil, fmt.Errorf("unsupported VLESS key length")
			}
			keys = append(keys, base64.RawURLEncoding.EncodeToString(public))
		}
		encryption := strings.Join(append([]string{v.Encryption, v.EncryptionSettings.Mode, "0rtt"}, keys...), ".")
		out := map[string]any{"protocol": "vless", "tag": "proxy", "settings": map[string]any{"vnext": []any{map[string]any{"address": server, "port": info.Common.ServerPort, "users": []any{map[string]any{"id": user.Uuid, "encryption": encryption}}}}}, "mux": map[string]any{"enabled": true, "concurrency": 4}, "streamSettings": map[string]any{"network": "xdrive", "xdriveSettings": settings}}
		return json.MarshalIndent(map[string]any{"outbounds": []any{out}}, "", "  ")
	default:
		return nil, fmt.Errorf("this node type has no extended client exporter")
	}
}
func clientTLS(info *panel.NodeInfo, options *conf.Options, server string) map[string]any {
	name := info.Common.ServerName
	if name == "" {
		if options != nil && options.CertConfig != nil {
			name = options.CertConfig.CertDomain
		}
	}
	if name == "" {
		name = server
	}
	result := map[string]any{"serverName": name}
	if options != nil && options.CertConfig != nil {
		cert := options.CertConfig
		if len(cert.ALPN) > 0 {
			result["alpn"] = cert.ALPN
		}
		if cert.CertMode == "self" {
			if data, err := os.ReadFile(cert.CertFile); err == nil {
				result["certificates"] = []any{map[string]any{"usage": "verify", "certificate": strings.Split(strings.TrimSpace(string(data)), "\n")}}
			}
		}
	}
	return result
}
