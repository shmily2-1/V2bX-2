package xray

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
	xconf "github.com/xtls/xray-core/infra/conf"
)

func TestRejectedLegacyKCPOptionsAndUnknownTransport(t *testing.T) {
	for _, data := range []string{`{"seed":"not-applied-by-this-version"}`, `{"header":{"type":"srtp"}}`, `{"congestion":true}`} {
		if _, err := buildStream("kcp", json.RawMessage(data)); err == nil {
			t.Fatal("silently ignored KCP options")
		}
	}
	if _, err := buildStream("unknown", nil); err == nil {
		t.Fatal("silently accepted unknown transport")
	}
	stream, err := buildStream("mkcp", json.RawMessage(`{"mtu":1200,"tti":20,"cwndMultiplier":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if stream.KCPSettings == nil || stream.KCPSettings.Mtu == nil || *stream.KCPSettings.Mtu != 1200 {
		t.Fatal("KCP settings were not parsed")
	}
}

func TestFallbackDoesNotDisableVLESSEncryption(t *testing.T) {
	opts := &conf.Options{XrayOptions: conf.NewXrayOptions()}
	opts.XrayOptions.EnableFallback = true
	err := buildV2ray(opts, &panel.NodeInfo{Type: "vless", VAllss: &panel.VAllssNode{Encryption: "mlkem768x25519plus"}}, &xconf.InboundDetourConfig{})
	if err == nil || !strings.Contains(err.Error(), "refusing to disable encryption") {
		t.Fatalf("unexpected result: %v", err)
	}
}
