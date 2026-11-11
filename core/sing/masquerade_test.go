package sing

import (
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
)

func TestHysteria2MasqueradeURL(t *testing.T) {
	info := &panel.NodeInfo{
		Type:      "hysteria2",
		Security:  panel.Tls,
		Common:    &panel.CommonNode{ServerPort: 443},
		Hysteria2: &panel.Hysteria2Node{},
	}

	t.Run("configured proxy site", func(t *testing.T) {
		config := &conf.Options{
			Core:        "sing",
			ListenIP:    "0.0.0.0",
			SingOptions: conf.NewSingOptions(),
			CertConfig:  &conf.CertConfig{CertMode: "file", CertFile: "/etc/V2bX/fullchain.pem", KeyFile: "/etc/V2bX/privkey.pem"},
		}
		config.SingOptions.Hysteria2MasqueradeURL = "https://www.example.test/landing"
		inbound, err := getInboundOptions("test", info, config)
		if err != nil {
			t.Fatal(err)
		}
		options, ok := inbound.Options.(*option.Hysteria2InboundOptions)
		if !ok || options.Masquerade == nil {
			t.Fatalf("expected Hysteria2 masquerade options, got %#v", inbound.Options)
		}
		if options.Masquerade.Type != "proxy" || options.Masquerade.ProxyOptions.URL != config.SingOptions.Hysteria2MasqueradeURL {
			t.Fatalf("unexpected masquerade options: %#v", options.Masquerade)
		}
	})

	t.Run("invalid URL rejected", func(t *testing.T) {
		config := &conf.Options{
			Core:        "sing",
			ListenIP:    "0.0.0.0",
			SingOptions: conf.NewSingOptions(),
			CertConfig:  &conf.CertConfig{CertMode: "file", CertFile: "/etc/V2bX/fullchain.pem", KeyFile: "/etc/V2bX/privkey.pem"},
		}
		config.SingOptions.Hysteria2MasqueradeURL = "ftp://www.example.test"
		if _, err := getInboundOptions("test", info, config); err == nil {
			t.Fatal("expected unsupported masquerade scheme to fail")
		}
	})
}
