package porthop

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []Range
	}{
		{"", nil}, {"  ", nil}, {"20000-20010", []Range{{20000, 20010}}},
		{" 22000 , 20000-20010,20005-20020,20021 ", []Range{{20000, 20021}, {22000, 22000}}},
		{"1,65534-65535,65535", []Range{{1, 1}, {65534, 65535}}},
	} {
		got, err := Parse(tc.input)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q: %v %v", tc.input, got, err)
		}
	}
	for _, input := range []string{"0", "65536", "20010-20000", "-1", "1-", "1-2-3", "all", "*", "1,,2", "1; reboot", "+1", "2:3", strings.Repeat("1,", 128) + "1"} {
		if _, err := Parse(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestExcludeListeningPort(t *testing.T) {
	for _, tc := range []struct {
		port uint16
		want []Range
	}{
		{8443, []Range{{20000, 20010}}}, {20000, []Range{{20001, 20010}}},
		{20010, []Range{{20000, 20009}}}, {20005, []Range{{20000, 20004}, {20006, 20010}}},
	} {
		ranges := []Range{{20000, 20010}}
		if got := withoutPort(ranges, tc.port); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%d: %v", tc.port, got)
		}
		if ranges[0] != (Range{20000, 20010}) {
			t.Fatal("mutated input")
		}
	}
	if got := withoutPort([]Range{{65535, 65535}}, 65535); len(got) != 0 {
		t.Fatal(got)
	}
}

type command struct {
	input, name string
	args        []string
}
type fakeRunner struct {
	commands []command
	missing  map[string]bool
	failAt   int
}

func (r *fakeRunner) LookPath(name string) (string, error) {
	if r.missing[name] {
		return "", errors.New("not installed")
	}
	return name, nil
}
func (r *fakeRunner) Run(input, name string, args ...string) error {
	r.commands = append(r.commands, command{input, name, append([]string(nil), args...)})
	if len(r.commands) == r.failAt {
		return errors.New("injected failure")
	}
	return nil
}
func (r *fakeRunner) text() string {
	var lines []string
	for _, c := range r.commands {
		lines = append(lines, c.name+" "+strings.Join(c.args, " ")+" "+c.input)
	}
	return strings.Join(lines, "\n")
}
func requireContains(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Fatalf("missing %q in %s", part, text)
		}
	}
}

func TestNFTAtomicDualStackAndCleanup(t *testing.T) {
	r := &fakeRunner{}
	c, err := setup(r, "linux", "auto", "test; not a command", "::", 20005, "20000-20010")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.commands) != 1 || !reflect.DeepEqual(r.commands[0].args, []string{"-f", "-"}) {
		t.Fatal("not atomic")
	}
	text := r.text()
	requireContains(t, text, "create table ip v2bx_hop_", "create table ip6 v2bx_hop_", "hook prerouting", "hook output", "fib daddr type local", "udp dport 20000-20004", "udp dport 20006-20010", "redirect to :20005")
	if strings.Contains(text, "udp dport 20005") || strings.Contains(text, "not a command") {
		t.Fatal("listen port or tag leaked into rules")
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	if len(r.commands) != 3 || r.commands[1].args[2] != "ip6" || r.commands[2].args[2] != "ip" {
		t.Fatal("wrong cleanup order")
	}
	if err = c.Close(); err != nil || len(r.commands) != 3 {
		t.Fatal("cleanup not idempotent")
	}
}

func TestFirewallAddressScoping(t *testing.T) {
	for _, backend := range []string{"nftables", "iptables"} {
		for _, ip := range []string{"0.0.0.0", "192.0.2.12", "2001:db8::12"} {
			t.Run(backend+ip, func(t *testing.T) {
				r := &fakeRunner{}
				c, err := setup(r, "linux", backend, "node", ip, 8443, "20000-20010")
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				text := r.text()
				if ip == "0.0.0.0" {
					if strings.Contains(text, "ip6") {
						t.Fatal("IPv4-only bind changed IPv6")
					}
					if backend == "nftables" {
						requireContains(t, text, "fib daddr type local", "redirect to :8443")
					} else {
						requireContains(t, text, "--dst-type LOCAL", "--to-ports 8443")
					}
				} else if backend == "nftables" {
					requireContains(t, text, "daddr "+ip, "dnat to")
				} else {
					requireContains(t, text, "-d "+ip, "--to-destination")
				}
				if ip == "2001:db8::12" {
					requireContains(t, text, "[2001:db8::12]:8443")
				}
			})
		}
	}
}

func TestIPTablesFallbackAndCleanup(t *testing.T) {
	r := &fakeRunner{missing: map[string]bool{"nft": true}}
	c, err := setup(r, "linux", "auto", "node", "::", 8443, "20000-20010")
	if err != nil {
		t.Fatal(err)
	}
	requireContains(t, r.text(), "iptables -w 5 -t nat -N V2BX-HOP-", "ip6tables -w 5 -t nat -N V2BX-HOP-", "--dport 20000:20010 -j REDIRECT --to-ports 8443", "-A OUTPUT -p udp -m addrtype --dst-type LOCAL")
	before := len(r.commands)
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	var ops []string
	for _, cmd := range r.commands[before:] {
		ops = append(ops, cmd.name+" "+cmd.args[4])
	}
	want := []string{"ip6tables -D", "ip6tables -D", "ip6tables -F", "ip6tables -X", "iptables -D", "iptables -D", "iptables -F", "iptables -X"}
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("cleanup=%v", ops)
	}
}

func TestFailuresRollbackAndRetry(t *testing.T) {
	// Inject every creation failure for dual-stack iptables. Every created
	// chain must be flushed/deleted; no flush of PREROUTING/OUTPUT is permitted.
	for fail := 1; fail <= 8; fail++ {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			r := &fakeRunner{failAt: fail}
			c, err := setup(r, "linux", "iptables", "node", "::", 8443, "20000-20010")
			if err == nil || c != nil {
				t.Fatal("expected successful rollback after setup error")
			}
			for _, cmd := range r.commands[fail:] {
				if (cmd.args[4] == "-F" || cmd.args[4] == "-X") && !strings.HasPrefix(cmd.args[5], "V2BX-HOP-") {
					t.Fatal("cleanup touches foreign chain")
				}
			}
		})
	}
	r := &fakeRunner{}
	c, err := setup(r, "linux", "iptables", "node", "0.0.0.0", 8443, "20000")
	if err != nil {
		t.Fatal(err)
	}
	r.failAt = len(r.commands) + 1
	before := len(r.commands)
	if err = c.Close(); err == nil || len(r.commands) != before+1 {
		t.Fatal("cleanup should stop on failure")
	}
	if err = c.Close(); err != nil {
		t.Fatal("cleanup retry:", err)
	}
	r = &fakeRunner{failAt: 1}
	if c, err = setup(r, "linux", "nft", "node", "::", 8443, "20000"); err == nil || c != nil || len(r.commands) != 1 {
		t.Fatal("failed nft batch was not atomic")
	}
}

func TestNoSideEffectsForEmptyOrInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		os, backend, ip, ports string
		port                   int
		invalid                bool
	}{
		{"windows", "bogus", "", "", 0, false}, {"linux", "bogus", "", " ", 0, false},
		{"windows", "auto", "::", "20000-20010", 8443, true},
		{"linux", "auto", "::", "30000-20000", 8443, true},
		{"linux", "bad", "::", "20000", 8443, true},
		{"linux", "auto", "example.org", "20000", 8443, true},
		{"linux", "auto", "fe80::1%eth0", "20000", 8443, true},
		{"linux", "auto", "::", "20000", 0, true},
		{"linux", "auto", "::", "8443", 8443, false},
	} {
		r := &fakeRunner{}
		c, err := setup(r, tc.os, tc.backend, "node", tc.ip, tc.port, tc.ports)
		if (err != nil) != tc.invalid || c != nil || len(r.commands) != 0 {
			t.Fatalf("%+v: %v %v", tc, c, err)
		}
	}
	r := &fakeRunner{missing: map[string]bool{"nft": true, "ip6tables": true}}
	if _, err := setup(r, "linux", "auto", "node", "::", 8443, "20000"); err == nil || len(r.commands) > 0 {
		t.Fatal("tool availability must be checked before mutations")
	}
}
