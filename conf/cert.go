package conf

type CertConfig struct {
	CertMode         string            `json:"CertMode"` // none, file, http, dns
	RejectUnknownSni bool              `json:"RejectUnknownSni"`
	CertDomain       string            `json:"CertDomain"`
	CertFile         string            `json:"CertFile"`
	KeyFile          string            `json:"KeyFile"`
	Provider         string            `json:"Provider"` // alidns, cloudflare, gandi, godaddy....
	Email            string            `json:"Email"`
	DNSEnv           map[string]string `json:"DNSEnv"`
	ALPN             []string          `json:"ALPN,omitempty"`
	MinVersion       string            `json:"MinVersion,omitempty"`
	MaxVersion       string            `json:"MaxVersion,omitempty"`
	CipherSuites     []string          `json:"CipherSuites,omitempty"`
	ECHKeyFile       string            `json:"ECHKeyFile,omitempty"`
}

func NewCertConfig() *CertConfig {
	return &CertConfig{
		CertMode: "none",
	}
}
