package panel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shmily2-1/V2bX-2/conf"
)

func TestXboardHysteriaConfig(t *testing.T) {
	for _, tc := range []struct {
		name, nodeType, fields, want string
		invalid                      bool
	}{
		{"xboard-v2", "hysteria", `"version":2,"ports":"20000-30000","hop_interval":30`, "hysteria2", false},
		{"explicit-v2", "hysteria2", `"version":2,"ports":"20000-20010"`, "hysteria2", false},
		{"legacy-v2", "hysteria2", `"ports":null,"hop_interval":null`, "hysteria2", false},
		{"legacy-v1", "hysteria", `"up_mbps":100`, "hysteria", false},
		{"explicit-v1", "hysteria", `"version":1`, "hysteria", false},
		{"unpatched-xboard", "hysteria", `"version":2`, "hysteria2", false},
		{"bad-range", "hysteria", `"version":2,"ports":"30000-20000"`, "", true},
		{"bad-interval", "hysteria", `"version":2,"hop_interval":-1`, "", true},
		{"short-interval", "hysteria", `"version":2,"hop_interval":4`, "", true},
		{"unknown-version", "hysteria", `"version":3`, "", true},
		{"mismatched-version", "hysteria2", `"version":1`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"protocol":"hysteria","server_port":8443,"server_name":"localhost","obfs":"salamander","obfs-password":"test-only-secret",%s}`, tc.fields)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/server/UniProxy/config" || r.URL.Query().Get("node_type") != tc.nodeType {
					t.Errorf("unexpected request %s", r.URL)
				}
				w.Header().Set("ETag", `"test"`)
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			c, err := New(&conf.ApiConfig{APIHost: srv.URL, Key: "local-test", NodeType: tc.nodeType, NodeID: 1})
			if err != nil {
				t.Fatal(err)
			}
			node, err := c.GetNodeInfo()
			if tc.invalid {
				if err == nil {
					t.Fatal("expected validation error")
				}
				if c.nodeEtag != "" || c.responseBodyHash != "" {
					t.Fatal("invalid config cached")
				}
				if _, err = c.GetNodeInfo(); err == nil {
					t.Fatal("invalid config suppressed on retry")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if node.Type != tc.want || node.Common.ServerPort != 8443 || node.Security != Tls {
				t.Fatalf("bad node: %+v", node)
			}
			if node.PushInterval != time.Minute || node.PullInterval != time.Minute {
				t.Fatal("missing base_config did not default safely")
			}
			if tc.want == "hysteria2" {
				if node.Hysteria2 == nil || node.Hysteria != nil {
					t.Fatal("wrong protocol data")
				}
				if node.Hysteria2.ObfsPassword != "test-only-secret" {
					t.Fatal("lost obfs password")
				}
				if tc.name == "xboard-v2" && (node.Hysteria2.Ports != "20000-30000" || node.Hysteria2.HopInterval != 30) {
					t.Fatal("lost hopping fields")
				}
			} else if node.Hysteria == nil || node.Hysteria2 != nil {
				t.Fatal("lost Hysteria1 compatibility")
			}
			if c.NodeType != tc.nodeType {
				t.Fatal("changed panel query type")
			}
			if n, err := c.GetNodeInfo(); n != nil || err != nil {
				t.Fatalf("unchanged config: %v %v", n, err)
			}
			body = strings.Replace(body, "8443", "9443", 1)
			if n, err := c.GetNodeInfo(); err != nil || n == nil || n.Common.ServerPort != 9443 {
				t.Fatalf("changed config lost: %v %v", n, err)
			}
		})
	}
}

func TestNodeInfoTransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c, err := New(&conf.ApiConfig{APIHost: srv.URL, Key: "local", NodeType: "hysteria", NodeID: 1, Timeout: 1})
	if err != nil {
		t.Fatal(err)
	}
	c.client.SetRetryCount(0)
	if _, err = c.GetNodeInfo(); err == nil {
		t.Fatal("expected transport error, not panic")
	}
}
