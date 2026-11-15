package xray

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
	xnet "github.com/xtls/xray-core/common/net"
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

func TestBuildHysteria2XrayConfig(t *testing.T) {
	info := &panel.NodeInfo{
		Type:     "hysteria2",
		Security: panel.Tls,
		Hysteria2: &panel.Hysteria2Node{
			Version:            2,
			ObfsMode:           "gecko",
			ObfsPassword:       "test-password",
			ObfsMinPacketSize:  512,
			ObfsMaxPacketSize:  1200,
			UDPIDLETimeout:     45,
			Masquerade:         json.RawMessage(`{"type":"string","content":"ok","statusCode":200}`),
			QUICDisablePathMTU: true,
		},
	}
	in := &xconf.InboundDetourConfig{}
	if err := buildHysteria2(&conf.Options{}, info, in); err != nil {
		t.Fatal(err)
	}
	in.PortList = &xconf.PortList{Range: []xconf.PortRange{{From: 8443, To: 8443}}}
	in.ListenOn = &xconf.Address{Address: xnet.ParseAddress("127.0.0.1")}
	if in.Protocol != "hysteria" || in.StreamSetting == nil || in.StreamSetting.HysteriaSettings == nil {
		t.Fatalf("unexpected hysteria2 inbound: %+v", in)
	}
	if in.StreamSetting.HysteriaSettings.UdpIdleTimeout != 45 || in.StreamSetting.FinalMask == nil || len(in.StreamSetting.FinalMask.Udp) != 1 {
		t.Fatalf("hysteria2 settings not mapped: %+v", in.StreamSetting)
	}
	if _, err := in.Build(); err != nil {
		t.Fatalf("xray hysteria2 inbound did not build: %v", err)
	}
}
