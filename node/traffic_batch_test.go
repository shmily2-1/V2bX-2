package node

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/limiter"
)

func TestBatchLostAckRestartAndNewTraffic(t *testing.T) {
	limiter.Init()
	seen := map[string]bool{}
	total := int64(0)
	ack := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Traffic-Batch")
		if id == "" {
			t.Error("missing stable batch ID")
		}
		var rows map[int][]int64
		if err := json.NewDecoder(r.Body).Decode(&rows); err != nil {
			t.Error(err)
		}
		if !seen[id] {
			seen[id] = true
			total += rows[7][0]
		}
		if ack {
			w.Header().Set("X-Traffic-Ack", id)
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	api, err := panel.New(&conf.ApiConfig{APIHost: srv.URL, Key: "fixture", NodeType: "http", NodeID: 9})
	if err != nil {
		t.Fatal(err)
	}
	options := &conf.Options{TrafficStateDir: t.TempDir()}
	newController := func(rows []panel.UserTraffic) *Controller {
		c := NewController(&trafficCore{rows: rows}, api, options)
		c.tag = "batch-fixture"
		c.info = &panel.NodeInfo{Common: &panel.CommonNode{TrafficBatch: true}}
		c.limiter = limiter.AddLimiter(c.tag, &conf.LimitConfig{}, nil, nil)
		if err := c.loadTrafficState(); err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := newController([]panel.UserTraffic{{UID: 7, Upload: 100}})
	if err := c.reportUserTrafficTask(); err != nil {
		t.Fatal(err)
	}
	if c.inflightID == "" || total != 100 {
		t.Fatal("missing durable unacknowledged batch")
	}
	firstID := c.inflightID
	c = newController([]panel.UserTraffic{{UID: 7, Upload: 23}})
	if c.inflightID != firstID {
		t.Fatal("restart changed the batch ID")
	}
	ack = true
	if err := c.reportUserTrafficTask(); err != nil {
		t.Fatal(err)
	}
	if total != 100 || c.pendingTraffic[7].Upload != 23 {
		t.Fatal("retry billed twice or discarded new traffic")
	}
	if err := c.reportUserTrafficTask(); err != nil {
		t.Fatal(err)
	}
	if total != 123 || len(seen) != 2 || len(c.pendingTraffic) != 0 {
		t.Fatal("new traffic was not independently acknowledged")
	}
}
