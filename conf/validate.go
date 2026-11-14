package conf

import (
	"crypto/tls"
	"fmt"
)

func (c *Conf) Validate() error {
	if len(c.CoresConfig) == 0 && len(c.NodeConfig) > 0 {
		return fmt.Errorf("at least one core is required")
	}
	ids := map[string]bool{}
	for _, s := range c.LocalServices {
		if ids[s.ID] {
			return fmt.Errorf("duplicate local service ID")
		}
		ids[s.ID] = true
		if err := s.Validate(); err != nil {
			return fmt.Errorf("service %s: %w", s.ID, err)
		}
	}
	names := make(map[string]string)
	types := make(map[string]bool)
	for _, core := range c.CoresConfig {
		switch core.Type {
		case "xray", "sing", "hysteria2":
		default:
			return fmt.Errorf("unknown core type %q", core.Type)
		}
		name := core.Name
		if name == "" {
			name = core.Type
		}
		if _, exists := names[name]; exists {
			return fmt.Errorf("duplicate core name %q", name)
		}
		names[name] = core.Type
		types[core.Type] = true
	}
	nodes := make(map[string]bool)
	for i, node := range c.NodeConfig {
		key := fmt.Sprintf("%s|%s|%d", node.ApiConfig.APIHost, node.ApiConfig.NodeType, node.ApiConfig.NodeID)
		if nodes[key] {
			return fmt.Errorf("duplicate panel node at index %d", i)
		}
		nodes[key] = true
		if node.Options.CoreName != "" {
			if kind, ok := names[node.Options.CoreName]; !ok {
				return fmt.Errorf("node %d references unknown CoreName", i)
			} else if node.Options.Core != "" && node.Options.Core != kind {
				return fmt.Errorf("node %d: Core and CoreName select different types", i)
			}
		}
		if node.Options.Core != "" && !types[node.Options.Core] {
			return fmt.Errorf("node %d references an undeclared core type", i)
		}
		if err := node.Options.CertConfig.Validate(); err != nil {
			return fmt.Errorf("node %d: %w", i, err)
		}
		d := node.Options.LimitConfig
		if d.EnableDynamicSpeedLimit && (d.DynamicSpeedLimitConfig == nil || d.DynamicSpeedLimitConfig.Periodic <= 0 || d.DynamicSpeedLimitConfig.Traffic <= 0 || d.DynamicSpeedLimitConfig.SpeedLimit <= 0 || d.DynamicSpeedLimitConfig.ExpireTime <= 0) {
			return fmt.Errorf("node %d: dynamic speed limit values must be positive", i)
		}
	}
	return nil
}

func (c *CertConfig) Validate() error {
	if c == nil {
		return nil
	}
	versions := map[string]int{"": 0, "1.0": 10, "1.1": 11, "1.2": 12, "1.3": 13}
	min, ok := versions[c.MinVersion]
	if !ok {
		return fmt.Errorf("invalid TLS MinVersion %q", c.MinVersion)
	}
	max, ok := versions[c.MaxVersion]
	if !ok || (min > max && max != 0) {
		return fmt.Errorf("invalid TLS version range")
	}
	for _, alpn := range c.ALPN {
		if len(alpn) == 0 || len(alpn) > 255 {
			return fmt.Errorf("invalid ALPN value")
		}
	}
	suites := make(map[string]bool)
	for _, suite := range append(tls.CipherSuites(), tls.InsecureCipherSuites()...) {
		suites[suite.Name] = true
	}
	for _, name := range c.CipherSuites {
		if !suites[name] {
			return fmt.Errorf("unknown TLS cipher suite %q", name)
		}
		if name == "TLS_AES_128_GCM_SHA256" || name == "TLS_AES_256_GCM_SHA384" || name == "TLS_CHACHA20_POLY1305_SHA256" {
			return fmt.Errorf("TLS 1.3 cipher suites are not configurable")
		}
	}
	if c.ECHKeyFile != "" {
		if max != 0 && max < 13 {
			return fmt.Errorf("ECH requires TLS 1.3")
		}
		if _, err := c.ECHKeys(); err != nil {
			return err
		}
	}
	return nil
}
