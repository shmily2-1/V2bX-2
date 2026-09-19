package porthop

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const BackendEnv = "V2BX_FIREWALL_BACKEND"

type runner interface {
	LookPath(string) (string, error)
	Run(input, name string, args ...string) error
}

type commandRunner struct{}

func (commandRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (commandRunner) Run(input, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, args, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// Successful cleanup steps are removed; a failed step is retained for retry.
// Never flush a chain while one of our jumps is still attached to it.
type cleanup struct {
	mu    sync.Mutex
	steps []func() error
}

func (c *cleanup) add(fn func() error) { c.steps = append(c.steps, fn) }
func (c *cleanup) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.steps) > 0 {
		n := len(c.steps) - 1
		if err := c.steps[n](); err != nil {
			return fmt.Errorf("port hopping cleanup (retry or remove the named V2bX rule manually): %w", err)
		}
		c.steps = c.steps[:n]
	}
	return nil
}

// Setup is a no-op for empty ports, including on non-Linux hosts.
// ListenIP must be a literal local address. :: means the dual-stack wildcard;
// 0.0.0.0 means IPv4 only. Only explicit ports can change firewall rules.
func Setup(tag, listenIP string, listenPort int, ports string) (io.Closer, error) {
	return setup(commandRunner{}, runtime.GOOS, os.Getenv(BackendEnv), tag, listenIP, listenPort, ports)
}

func setup(r runner, goos, backend, tag, listenIP string, listenPort int, ports string) (io.Closer, error) {
	ranges, err := Parse(ports)
	if err != nil {
		return nil, err
	}
	if len(ranges) == 0 {
		return nil, nil
	}
	if goos != "linux" {
		return nil, fmt.Errorf("Hysteria2 port hopping requires Linux (received ports=%q)", ports)
	}
	if listenPort < 1 || listenPort > 65535 {
		return nil, fmt.Errorf("port hopping requires server_port in 1..65535")
	}
	addr, err := netip.ParseAddr(listenIP)
	if err != nil || addr.Zone() != "" {
		return nil, fmt.Errorf("port hopping requires a literal, unscoped ListenIP: %q", listenIP)
	}
	addr = addr.Unmap()
	ranges = withoutPort(ranges, uint16(listenPort))
	if len(ranges) == 0 {
		return nil, nil
	}
	// Stable, per-node names. Never adopt/flush an existing table or chain: it
	// might belong to a live process, or need inspection after a crash.
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", tag, addr, listenPort)))
	id := fmt.Sprintf("%x", hash[:8])
	backend = strings.ToLower(strings.TrimSpace(backend))
	switch backend {
	case "", "auto":
		if _, err := r.LookPath("nft"); err == nil {
			backend = "nftables"
		} else {
			backend = "iptables"
		}
	case "nft", "nftables":
		backend = "nftables"
	case "ipt", "iptables":
		backend = "iptables"
	default:
		return nil, fmt.Errorf("invalid %s: %q (use auto, nftables or iptables)", BackendEnv, backend)
	}
	if backend == "nftables" {
		return setupNFT(r, id, addr, listenPort, ranges)
	}
	return setupIPTables(r, id, addr, listenPort, ranges)
}

func families(addr netip.Addr) []string {
	if addr.Is4() {
		return []string{"ip"}
	}
	if addr.IsUnspecified() {
		return []string{"ip", "ip6"}
	}
	return []string{"ip6"}
}

func setupNFT(r runner, id string, addr netip.Addr, port int, ranges []Range) (io.Closer, error) {
	bin, err := r.LookPath("nft")
	if err != nil {
		return nil, fmt.Errorf("port hopping requires nft: %w", err)
	}
	name := "v2bx_hop_" + id
	var script strings.Builder
	for _, family := range families(addr) {
		fmt.Fprintf(&script, "create table %s %s\n", family, name)
		for _, chain := range []string{"prerouting", "output"} {
			fmt.Fprintf(&script, "add chain %s %s %s { type nat hook %s priority dstnat; policy accept; }\n", family, name, chain, chain)
			match := "fib daddr type local"
			target := fmt.Sprintf("redirect to :%d", port)
			if !addr.IsUnspecified() {
				match = family + " daddr " + addr.String()
				target = "dnat to " + netip.AddrPortFrom(addr, uint16(port)).String()
			}
			for _, ports := range ranges {
				fmt.Fprintf(&script, "add rule %s %s %s %s udp dport %s counter %s\n", family, name, chain, match, portExpr(ports, "-"), target)
			}
		}
	}
	// nft -f applies the batch atomically. 'create' (not 'add') fails on an
	// existing table without touching its contents.
	if err := r.Run(script.String(), bin, "-f", "-"); err != nil {
		return nil, fmt.Errorf("install %s (need CAP_NET_ADMIN/root; inspect stale tables after an unclean shutdown): %w", name, err)
	}
	c := &cleanup{}
	for _, family := range families(addr) {
		c.add(func() error { return r.Run("", bin, "delete", "table", family, name) })
	}
	return c, nil
}

func setupIPTables(r runner, id string, addr netip.Addr, port int, ranges []Range) (io.Closer, error) {
	var bins []string
	for _, family := range families(addr) {
		name := "iptables"
		if family == "ip6" {
			name = "ip6tables"
		}
		bin, err := r.LookPath(name)
		if err != nil {
			return nil, fmt.Errorf("port hopping requires %s: %w", name, err)
		}
		bins = append(bins, bin)
	}
	c := &cleanup{}
	rollback := func(err error) (io.Closer, error) {
		if cleanErr := c.Close(); cleanErr != nil {
			return c, errors.Join(err, cleanErr)
		}
		return nil, err
	}
	name := "V2BX-HOP-" + id
	for _, bin := range bins {
		run := func(args ...string) error {
			return r.Run("", bin, append([]string{"-w", "5", "-t", "nat"}, args...)...)
		}
		if err := run("-N", name); err != nil {
			return rollback(err)
		}
		c.add(func() error { return run("-X", name) })
		c.add(func() error { return run("-F", name) })
		for _, ports := range ranges {
			args := []string{"-A", name, "-p", "udp", "--dport", portExpr(ports, ":")}
			if addr.IsUnspecified() {
				args = append(args, "-j", "REDIRECT", "--to-ports", strconv.Itoa(port))
			} else {
				args = append(args, "-j", "DNAT", "--to-destination", netip.AddrPortFrom(addr, uint16(port)).String())
			}
			if err := run(args...); err != nil {
				return rollback(err)
			}
		}
		for _, chain := range []string{"PREROUTING", "OUTPUT"} {
			args := []string{"-A", chain, "-p", "udp"}
			if addr.IsUnspecified() {
				args = append(args, "-m", "addrtype", "--dst-type", "LOCAL")
			} else {
				args = append(args, "-d", addr.String())
			}
			args = append(args, "-j", name)
			if err := run(args...); err != nil {
				return rollback(err)
			}
			remove := append([]string{"-D"}, args[1:]...)
			c.add(func() error { return run(remove...) })
		}
	}
	return c, nil
}
