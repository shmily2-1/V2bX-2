package node

import (
	"encoding/json"
	"fmt"
	"github.com/juju/ratelimit"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/common/format"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/limiter"
)

type trafficCore struct {
	recordingCore
	rows []panel.UserTraffic
}

func (c *trafficCore) GetUserTrafficSlice(_ string, reset bool) ([]panel.UserTraffic, error) {
	rows := append([]panel.UserTraffic(nil), c.rows...)
	if reset {
		c.rows = nil
	}
	return rows, nil
}

func TestFailedTrafficReportSurvivesControllerRestart(t *testing.T) {
	limiter.Init()
	fail := true
	var received map[int][]int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(503)
			fmt.Fprint(w, "temporary failure")
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, "{}")
	}))
	defer srv.Close()
	api, err := panel.New(&conf.ApiConfig{APIHost: srv.URL, Key: "loopback-fixture", NodeType: "vless", NodeID: 7})
	if err != nil {
		t.Fatal(err)
	}
	options := &conf.Options{TrafficStateDir: t.TempDir()}
	newController := func(rows []panel.UserTraffic) *Controller {
		c := NewController(&trafficCore{rows: rows}, api, options)
		c.tag = "traffic-fixture"
		c.limiter = limiter.AddLimiter(c.tag, &conf.LimitConfig{}, nil, nil)
		if err := c.loadTrafficState(); err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := newController([]panel.UserTraffic{{UID: 7, Upload: 100, Download: 200}})
	if err := c.reportUserTrafficTask(); err != nil {
		t.Fatal(err)
	}
	if len(c.pendingTraffic) != 1 {
		t.Fatal("failure dropped pending traffic")
	}
	restarted := newController([]panel.UserTraffic{{UID: 7, Upload: 11, Download: 13}})
	fail = false
	if err := restarted.reportUserTrafficTask(); err != nil {
		t.Fatal(err)
	}
	if got := received[7]; len(got) != 2 || got[0] != 111 || got[1] != 213 {
		t.Fatalf("wrong recovered report: %v", received)
	}
	data, err := os.ReadFile(restarted.trafficStatePath)
	if err != nil {
		t.Fatal(err)
	}
	var state trafficState
	if err := json.Unmarshal(data, &state); err != nil || len(state.Pending) != 0 {
		t.Fatalf("acknowledgement not persisted: %s %v", data, err)
	}
}

func TestDynamicLimitCollectsTrafficAndPreservesAuthorizationAfterExpiry(t *testing.T) {
	limiter.Init()
	users := []panel.UserInfo{{Id: 7, Uuid: "dynamic-fixture"}}
	options := &conf.Options{LimitConfig: conf.LimitConfig{EnableDynamicSpeedLimit: true,
		DynamicSpeedLimitConfig: &conf.DynamicSpeedLimitConfig{Periodic: 1, Traffic: 100, SpeedLimit: 1, ExpireTime: 1}}}
	c := NewController(&trafficCore{rows: []panel.UserTraffic{{UID: 7, Upload: 120}}}, nil, options)
	c.tag, c.userList = "dynamic-fixture", users
	c.limiter = limiter.AddLimiter(c.tag, &options.LimitConfig, users, nil)
	c.apiClient, _ = panel.New(&conf.ApiConfig{APIHost: "http://127.0.0.1", NodeType: "vless", NodeID: 7})
	c.startTasks(&panel.NodeInfo{PullInterval: time.Hour, PushInterval: time.Hour})
	defer c.Close()
	key := format.UserTag(c.tag, users[0].Uuid)
	var bucket *ratelimit.Bucket
	var rejected bool
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		bucket, rejected = c.limiter.CheckLimit(key, "127.0.0.1", true, false)
		if bucket != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rejected || bucket == nil || bucket.Rate() != 125000 {
		t.Fatal("dynamic limit not applied")
	}
	if err := c.limiter.UpdateDynamicSpeedLimit(c.tag, users[0].Uuid, 1, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	bucket, rejected = c.limiter.CheckLimit(key, "127.0.0.1", true, false)
	if rejected || bucket != nil {
		t.Fatal("expiry removed authorization or retained speed limit")
	}
}

func TestDeviceLimitAndUIDChangesAreApplied(t *testing.T) {
	old := []panel.UserInfo{{Id: 7, Uuid: "same-user", DeviceLimit: 1}}
	for _, updated := range []panel.UserInfo{{Id: 7, Uuid: "same-user", DeviceLimit: 2}, {Id: 8, Uuid: "same-user", DeviceLimit: 1}} {
		deleted, added := compareUserList(old, []panel.UserInfo{updated})
		if len(deleted) != 1 || len(added) != 1 || added[0] != updated {
			t.Fatalf("missed user change: %v %v", deleted, added)
		}
	}
}

func TestSmallTrafficAccumulatesUntilReportMinimum(t *testing.T) {
	limiter.Init()
	reports := make(chan map[int][]int64, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[int][]int64
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		reports <- body
		fmt.Fprint(w, "{}")
	}))
	defer srv.Close()
	api, err := panel.New(&conf.ApiConfig{APIHost: srv.URL, NodeType: "vless", NodeID: 7})
	if err != nil {
		t.Fatal(err)
	}
	engine := &trafficCore{rows: []panel.UserTraffic{{UID: 7, Upload: 512}}}
	c := NewController(engine, api, &conf.Options{ReportMinTraffic: 1})
	c.tag = "minimum-fixture"
	c.limiter = limiter.AddLimiter(c.tag, &conf.LimitConfig{}, nil, nil)
	if err := c.reportUserTrafficTask(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reports:
		t.Fatal("reported before minimum")
	default:
	}
	if c.pendingTraffic[7].Upload != 512 {
		t.Fatal("discarded small traffic")
	}
	engine.rows = []panel.UserTraffic{{UID: 7, Upload: 600}, {UID: 8, Download: 100}}
	if err := c.reportUserTrafficTask(); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-reports:
		if len(body) != 1 || body[7][0] != 1112 {
			t.Fatal(body)
		}
	default:
		t.Fatal("did not report accumulated traffic")
	}
	if len(c.pendingTraffic) != 1 || c.pendingTraffic[8].Download != 100 {
		t.Fatal("discarded unreported smaller user")
	}
}
