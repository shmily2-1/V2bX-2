package conf

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFailureKeepsPreviousConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"Cores":[{"Type":"xray"}],"Log":{"Level":"warn"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c := New()
	if err := c.LoadFromPath(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"Cores":[{"Type":"unknown"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.LoadFromPath(path); err == nil {
		t.Fatal("accepted unsupported core")
	}
	if c.LogConfig.Level != "warn" || c.CoresConfig[0].Type != "xray" {
		t.Fatal("failed load damaged previous configuration")
	}
}

func TestWatcherSurvivesAtomicReplacementAndRejectedApply(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	write := func(level string) {
		t.Helper()
		temp := filepath.Join(dir, "next.json")
		if err := os.WriteFile(temp, []byte(`{"Cores":[{"Type":"xray"}],"Log":{"Level":"`+level+`"}}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temp, path); err != nil {
			t.Fatal(err)
		}
	}
	write("info")
	c := New()
	if err := c.LoadFromPath(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	levels := make(chan string, 4)
	if err := c.WatchContext(ctx, path, "", "", func(candidate *Conf) error {
		levels <- candidate.LogConfig.Level
		return errors.New("intentional rejected apply")
	}); err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"warn", "debug"} {
		write(level)
		select {
		case got := <-levels:
			if got != level {
				t.Fatalf("got %s want %s", got, level)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("missed atomic replacement")
		}
	}
	if c.LogConfig.Level != "info" {
		t.Fatal("rejected apply committed new configuration")
	}
	if err := os.WriteFile(path, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-levels:
		t.Fatal("invalid configuration reached apply")
	case <-time.After(500 * time.Millisecond):
	}
}

func TestTLSSettingsValidation(t *testing.T) {
	for _, cert := range []*CertConfig{{MinVersion: "1.3", MaxVersion: "1.2"}, {MinVersion: "bad"}, {ALPN: []string{""}}, {CipherSuites: []string{"UNKNOWN"}}, {CipherSuites: []string{"TLS_AES_128_GCM_SHA256"}}} {
		if err := cert.Validate(); err == nil {
			t.Fatalf("accepted invalid TLS configuration: %+v", cert)
		}
	}
	if err := (&CertConfig{MinVersion: "1.2", MaxVersion: "1.3", ALPN: []string{"h2"}, CipherSuites: []string{"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256"}}).Validate(); err != nil {
		t.Fatal(err)
	}
}
