package node

import (
	"fmt"
	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/core"
	"github.com/shmily2-1/V2bX-2/limiter"
	"net/http"
	"net/http/httptest"
	"testing"
)

type recordingCore struct {
	core.Core
	nodes, adds, deletes int
}

func (c *recordingCore) AddNode(string, *panel.NodeInfo, *conf.Options) error { c.nodes++; return nil }
func (c *recordingCore) DelNode(string) error                                 { c.nodes--; return nil }
func (c *recordingCore) AddUsers(p *core.AddUsersParams) (int, error) {
	if len(p.Users) == 0 {
		panic("empty AddUsers")
	}
	c.adds += len(p.Users)
	return len(p.Users), nil
}
func (c *recordingCore) DelUsers(users []panel.UserInfo, _ string, _ *panel.NodeInfo) error {
	c.deletes += len(users)
	return nil
}

func TestEmptyUserStartAssign304AndRevoke(t *testing.T) {
	limiter.Init()
	body := "{\"users\":[]}"
	unchanged := false
	port := 443
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/server/UniProxy/config":
			fmt.Fprintf(w, "{\"protocol\":\"hysteria\",\"version\":2,\"server_port\":%d,\"base_config\":{\"pull_interval\":3600,\"push_interval\":3600}}", port)
		case "/api/v1/server/UniProxy/user":
			if unchanged {
				w.WriteHeader(304)
			} else {
				fmt.Fprint(w, body)
			}
		case "/api/v1/server/UniProxy/alivelist":
			fmt.Fprint(w, "{\"alive\":{}}")
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	api, err := panel.New(&conf.ApiConfig{APIHost: srv.URL, Key: "loopback-test", NodeType: "hysteria", NodeID: 1})
	if err != nil {
		t.Fatal(err)
	}
	engine := &recordingCore{}
	controller := NewController(engine, api, &conf.Options{Core: "sing", CertConfig: &conf.CertConfig{CertMode: "none"}})
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	if engine.nodes != 1 || engine.adds != 0 {
		t.Fatal("empty-user start did not wait safely")
	}
	body = "{\"users\":[{\"id\":7,\"uuid\":\"039e9cbd-c073-457c-a25b-eed489b564c9\"}]}"
	controller.nodeInfoMonitor()
	if engine.adds != 1 || len(controller.userList) != 1 {
		t.Fatal("assignment not applied")
	}
	unchanged = true
	controller.nodeInfoMonitor()
	if engine.deletes != 0 || len(controller.userList) != 1 {
		t.Fatal("304 revoked user")
	}
	port = 444
	controller.nodeInfoMonitor()
	if engine.nodes != 1 || len(controller.userList) != 1 {
		t.Fatal("304 plus node refresh lost users")
	}
	unchanged, body = false, "{\"users\":[]}"
	controller.nodeInfoMonitor()
	if engine.deletes != 1 || len(controller.userList) != 0 {
		t.Fatal("empty response did not revoke all users")
	}
	port = 445
	controller.nodeInfoMonitor()
	if engine.nodes != 1 || len(controller.userList) != 0 {
		t.Fatal("empty config reload failed")
	}
}
