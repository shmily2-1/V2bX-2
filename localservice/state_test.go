package localservice

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shmily2-1/V2bX-2/conf"
)

func testKey(t *testing.T) string {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k.PublicKey().Bytes())
}
func TestStablePeerAllocation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "addresses.json")
	s := conf.LocalService{ID: "allocation", Type: "wireguard", Listen: "0.0.0.0:51820", WireGuard: &conf.WireGuardService{PrivateKeyFile: "unused.key", Address: []string{"10.0.0.1/29", "fd00::1/125"}, Peers: []conf.WireGuardPeer{{UID: 1, PublicKey: testKey(t)}, {UID: 2, PublicKey: testKey(t)}}}}
	first, state, err := Allocate(s, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = WriteJSON(path, state); err != nil {
		t.Fatal(err)
	}
	s.WireGuard.Peers = append([]conf.WireGuardPeer{{UID: 3, PublicKey: testKey(t)}}, s.WireGuard.Peers...)
	next, _, err := Allocate(s, path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if strings.Join(first.WireGuard.Peers[i].Address, ",") != strings.Join(next.WireGuard.Peers[i+1].Address, ",") {
			t.Fatal("list reordering reassigned a surviving peer")
		}
	}
	if next.WireGuard.Peers[0].Address[0] == first.WireGuard.Peers[0].Address[0] {
		t.Fatal("new peer stole a reservation")
	}
	retained := s.WireGuard.Peers[2]
	retained.PublicKey = testKey(t)
	s.WireGuard.Peers = []conf.WireGuardPeer{{UID: 4, PublicKey: testKey(t)}, retained}
	reused, _, err := Allocate(s, path)
	if err != nil {
		t.Fatal(err)
	}
	if reused.WireGuard.Peers[0].Address[0] != first.WireGuard.Peers[0].Address[0] || reused.WireGuard.Peers[1].Address[0] != first.WireGuard.Peers[1].Address[0] {
		t.Fatal("removed address was not recycled, or key rotation changed a surviving UID address")
	}
	s.WireGuard.Peers = append(s.WireGuard.Peers, conf.WireGuardPeer{UID: 5, PublicKey: testKey(t)})
	s.WireGuard.Address = []string{"10.0.0.1/30"}
	if _, _, err = Allocate(s, path); err == nil {
		t.Fatal("exhausted pool was accepted")
	}
}

func TestBackendProcessHelper(t *testing.T) {
	if os.Getenv("V2BX_BACKEND_TEST_HELPER") != "1" {
		return
	}
	time.Sleep(time.Minute)
}
func TestBackendProcessSupervisor(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	t.Setenv("V2BX_BACKEND_TEST_HELPER", "1")
	process, err := startChild(&conf.BackendService{Executable: executable, SHA256: hex.EncodeToString(sum[:])}, "-test.run=TestBackendProcessHelper")
	if err != nil {
		t.Fatal(err)
	}
	if err = process.Health(); err != nil {
		t.Fatal(err)
	}
	if err = process.Close(); err != nil {
		t.Fatal(err)
	}
	if err = process.Health(); err == nil {
		t.Fatal("exited backend reported healthy")
	}
}
func TestServiceStoreAndBackup(t *testing.T) {
	c := &conf.Conf{LocalServiceDir: t.TempDir()}
	s := conf.LocalService{ID: "store", Type: "tunnel", Listen: "127.0.0.1:12411", Target: "127.0.0.1:12412", Network: "tcp"}
	if err := Edit(c, s.ID, func(target *conf.LocalService) error { *target = s; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := Edit(c, s.ID, func(target *conf.LocalService) error { target.Enabled = true; return nil }); err != nil {
		t.Fatal(err)
	}
	specs, err := LoadSpecs(c)
	if err != nil || len(specs) != 1 || !specs[0].Enabled {
		t.Fatalf("saved configuration missing: %v", err)
	}
	backups, _ := filepath.Glob(filepath.Join(c.LocalServiceDir, "store.json.bak-*"))
	if len(backups) != 1 {
		t.Fatal("original config was not backed up")
	}
	if err = Edit(c, s.ID, func(target *conf.LocalService) error { target.Type = ""; return nil }); err != nil {
		t.Fatal(err)
	}
	specs, err = LoadSpecs(c)
	if err != nil || len(specs) != 0 {
		t.Fatal("removal failed")
	}
	if err = Edit(c, "../escape", func(*conf.LocalService) error { return nil }); err == nil {
		t.Fatal("unsafe service ID accepted")
	}
}
func TestPinnedBackendAndMTGContract(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "backend.bin")
	os.WriteFile(executable, []byte("fixture"), 0600)
	sum := sha256.Sum256([]byte("fixture"))
	b := &conf.BackendService{Executable: executable, SHA256: hex.EncodeToString(sum[:])}
	if err := verifyBackend(b); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(executable, []byte("changed"), 0600)
	if err := verifyBackend(b); err == nil {
		t.Fatal("changed backend accepted")
	}
	secretPath := filepath.Join(root, "secret")
	secret := "ee" + strings.Repeat("01", 16) + hex.EncodeToString([]byte("example.invalid"))
	os.WriteFile(secretPath, []byte(secret), 0600)
	s := conf.LocalService{ID: "mtg", Type: "mtproto", Listen: "127.0.0.1:14221", Backend: &conf.BackendService{SecretFile: secretPath, MetricsListen: "127.0.0.1:14222", Concurrency: 12}}
	data, err := mtgConfig(s)
	if err != nil || !strings.Contains(string(data), "[stats.prometheus]") {
		t.Fatal("MTG config contract failed")
	}
	var up, down int64 = 31, 47
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, "v2bxmtg_telegram_traffic{dc=\"1\",direction=\"from_client\",telegram_ip=\"192.0.2.1\"} %d\nv2bxmtg_telegram_traffic{dc=\"1\",direction=\"to_client\",telegram_ip=\"192.0.2.1\"} %d\n", up, down)
	}))
	defer server.Close()
	s.Backend.MetricsListen = strings.TrimPrefix(server.URL, "http://")
	adapter := &mtprotoBackend{spec: s, previous: map[string]int64{}, client: server.Client()}
	rows, err := adapter.Traffic()
	if err != nil || len(rows) != 1 || rows[0].UID != 0 || rows[0].Upload != 31 || rows[0].Download != 47 {
		t.Fatalf("metric accounting failed: %+v %v", rows, err)
	}
	rows, err = adapter.Traffic()
	if err != nil || rows[0].Upload != 0 || rows[0].Download != 0 {
		t.Fatal("cumulative metrics counted twice")
	}
	up, down = 4, 5
	rows, err = adapter.Traffic()
	if err != nil || rows[0].Upload != 4 || rows[0].Download != 5 {
		t.Fatal("metric reset epoch was lost")
	}
}
