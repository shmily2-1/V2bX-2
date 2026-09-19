package panel

import (
	"fmt"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/vmihailenco/msgpack/v5"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUserListEmptyAndNotModified(t *testing.T) {
	for _, encoding := range []string{"json", "msgpack"} {
		t.Run(encoding, func(t *testing.T) {
			unchanged := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if unchanged {
					w.WriteHeader(304)
					return
				}
				w.Header().Set("ETag", "empty")
				if encoding == "msgpack" {
					w.Header().Set("Content-Type", "application/x-msgpack")
					msgpack.NewEncoder(w).Encode(UserListBody{Users: []UserInfo{}})
				} else {
					fmt.Fprint(w, "{\"users\":[]}")
				}
			}))
			defer srv.Close()
			c, err := New(&conf.ApiConfig{APIHost: srv.URL, Key: "loopback-test", NodeType: "hysteria", NodeID: 1})
			if err != nil {
				t.Fatal(err)
			}
			users, err := c.GetUserList()
			if err != nil || users == nil || len(users) != 0 {
				t.Fatalf("empty list must be non-nil: %v %v", users, err)
			}
			unchanged = true
			users, err = c.GetUserList()
			if err != nil || users != nil {
				t.Fatalf("304 must be nil: %v %v", users, err)
			}
		})
	}
}

func TestOnlineReportPropagatesPanelFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer srv.Close()
	c, err := New(&conf.ApiConfig{APIHost: srv.URL, Key: "loopback-test", NodeType: "hysteria", NodeID: 1})
	if err != nil {
		t.Fatal(err)
	}
	data := map[int][]string{1: {"127.0.0.1"}}
	if err := c.ReportNodeOnlineUsers(&data); err == nil {
		t.Fatal("online report failure falsely returned success")
	}
}

func TestMalformedUserResponseNotCached(t *testing.T) {
	for _, body := range []string{"{}", "{\"users\":null}", "{\"users\":[]", "{\"wrapper\":{\"users\":[]}}"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("ETag", "bad"); fmt.Fprint(w, body) }))
		c, err := New(&conf.ApiConfig{APIHost: srv.URL, Key: "loopback-test", NodeType: "hysteria", NodeID: 1})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.GetUserList(); err == nil {
			t.Errorf("accepted malformed response %q", body)
		}
		if c.userEtag != "" {
			t.Error("cached malformed user response")
		}
		srv.Close()
	}
}
