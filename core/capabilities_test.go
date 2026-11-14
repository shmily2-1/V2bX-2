package core

import (
	"encoding/json"
	"testing"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
)

type selectionCore struct {
	Core
	kind  string
	added int
}

func (s *selectionCore) Type() string                                         { return s.kind }
func (s *selectionCore) Protocols() []string                                  { return []string{"vless"} }
func (s *selectionCore) AddNode(string, *panel.NodeInfo, *conf.Options) error { s.added++; return nil }

func TestSelectorUsesConfiguredOrderAndCompatibleTransport(t *testing.T) {
	for _, transport := range []string{"tcp", "xhttp"} {
		for range 20 {
			sing, xray := &selectionCore{kind: "sing"}, &selectionCore{kind: "xray"}
			s := &Selector{cores: map[string]Core{"sing": sing, "xray": xray}, order: []string{"sing", "xray"}}
			info := &panel.NodeInfo{Type: "vless", Common: &panel.CommonNode{}, VAllss: &panel.VAllssNode{Network: transport}}
			opts := &conf.Options{RawOptions: json.RawMessage(`{}`)}
			if err := s.AddNode("test", info, opts); err != nil {
				t.Fatal(err)
			}
			if transport == "tcp" && (sing.added != 1 || xray.added != 0) {
				t.Fatal("non-deterministic selection")
			}
			if transport == "xhttp" && (sing.added != 0 || xray.added != 1) {
				t.Fatal("failed to select compatible core")
			}
		}
	}
}

func TestUnsupportedNamedCoreDoesNotSilentlyFallback(t *testing.T) {
	s := &Selector{cores: map[string]Core{"named": &selectionCore{kind: "sing"}}}
	info := &panel.NodeInfo{Type: "vless", Common: &panel.CommonNode{}, VAllss: &panel.VAllssNode{Network: "xhttp"}}
	if err := s.AddNode("test", info, &conf.Options{CoreName: "named"}); err == nil {
		t.Fatal("accepted unsupported named core")
	}
}

func TestAutomaticCoreKeepsResolvedGenericPaths(t *testing.T) {
	s := &Selector{cores: map[string]Core{"xray": &selectionCore{kind: "xray"}}, order: []string{"xray"}}
	info := &panel.NodeInfo{Type: "vless", Common: &panel.CommonNode{}, VAllss: &panel.VAllssNode{Network: "tcp"}}
	opts := &conf.Options{TrafficStateDir: "resolved-traffic", CertConfig: &conf.CertConfig{CertFile: "resolved-cert"}, RawOptions: json.RawMessage(`{"TrafficStateDir":"relative-traffic","CertConfig":{"CertFile":"relative-cert"},"DisableSniffing":true}`)}
	if err := s.AddNode("test", info, opts); err != nil {
		t.Fatal(err)
	}
	if opts.TrafficStateDir != "resolved-traffic" || opts.CertConfig.CertFile != "resolved-cert" || !opts.XrayOptions.DisableSniffing {
		t.Fatal("core resolution overwrote common paths or lost core settings")
	}
}
