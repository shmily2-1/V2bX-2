package localservice

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shmily2-1/V2bX-2/conf"
)

func init() { Register("mtproto", openMTProto) }

type mtprotoBackend struct {
	process  *childProcess
	spec     conf.LocalService
	previous map[string]int64
	client   *http.Client
}

func mtgConfig(s conf.LocalService) ([]byte, error) {
	secret, err := ReadSecret(s.Backend.SecretFile)
	if err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(secret)
	if err != nil || len(decoded) < 18 || decoded[0] != 0xee {
		return nil, fmt.Errorf("mtg requires a hex-encoded FakeTLS secret")
	}
	text := "debug = false\nsecret = " + strconv.Quote(secret) + "\nbind-to = " + strconv.Quote(s.Listen) + "\nconcurrency = " + strconv.Itoa(s.Backend.Concurrency) + "\n[defense.blocklist]\nenabled = false\n[defense.allowlist]\nenabled = false\n[stats.prometheus]\nenabled = true\nbind-to = " + strconv.Quote(s.Backend.MetricsListen) + "\nhttp-path = \"/metrics\"\nmetric-prefix = \"v2bxmtg\"\n"
	return []byte(text), nil
}
func openMTProto(s conf.LocalService, state string) (Instance, error) {
	config, err := mtgConfig(s)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(state, s.ID+".mtg.toml")
	if err = WritePrivate(path, config); err != nil {
		return nil, err
	}
	process, err := startChild(s.Backend, "run", path)
	if err != nil {
		return nil, err
	}
	b := &mtprotoBackend{process: process, spec: s, previous: map[string]int64{}, client: &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err = b.Health(); err == nil {
			return b, nil
		}
		if process.Health() != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = process.Close()
	return nil, fmt.Errorf("MTProto backend failed readiness checks")
}
func (b *mtprotoBackend) Close() error                 { b.client.CloseIdleConnections(); return b.process.Close() }
func (b *mtprotoBackend) ProcessStatus() ProcessStatus { return b.process.Status() }
func (b *mtprotoBackend) Health() error {
	if err := b.process.Health(); err != nil {
		return err
	}
	host, port, _ := net.SplitHostPort(b.spec.Listen)
	if host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 200*time.Millisecond)
	if err != nil {
		return fmt.Errorf("MTProto listen port unavailable")
	}
	c.Close()
	_, err = b.metrics()
	return err
}
func (b *mtprotoBackend) metrics() (map[string]int64, error) {
	r, err := b.client.Get("http://" + b.spec.Backend.MetricsListen + "/metrics")
	if err != nil {
		return nil, fmt.Errorf("MTProto metrics unavailable")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("MTProto metrics HTTP %d", r.StatusCode)
	}
	scanner := bufio.NewScanner(io.LimitReader(r.Body, 4<<20))
	result := map[string]int64{}
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "v2bxmtg_telegram_traffic{") && !strings.HasPrefix(line, "v2bxmtg_telegram_traffic_total{") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid MTProto traffic metric")
		}
		n, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || n < 0 || math.IsNaN(n) || math.IsInf(n, 0) || n >= math.MaxInt64 {
			return nil, fmt.Errorf("invalid MTProto traffic counter")
		}
		result[fields[0]] = int64(n)
	}
	if err = scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
func (b *mtprotoBackend) Traffic() ([]Traffic, error) {
	current, err := b.metrics()
	if err != nil {
		return nil, err
	}
	var traffic Traffic
	for key, n := range current {
		delta := n - b.previous[key]
		if delta < 0 {
			delta = n
		}
		if strings.Contains(key, `direction="from_client"`) {
			traffic.Upload += delta
		} else if strings.Contains(key, `direction="to_client"`) {
			traffic.Download += delta
		}
	}
	b.previous = current
	return []Traffic{traffic}, nil
}
