package sing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/juju/ratelimit"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	wgtransport "github.com/sagernet/sing-box/transport/wireguard"
	tun "github.com/sagernet/sing-tun"
	sjson "github.com/sagernet/sing/common/json"
	N "github.com/sagernet/sing/common/network"
	"github.com/shmily2-1/V2bX-2/common/counter"
	"github.com/shmily2-1/V2bX-2/common/rate"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/localservice"
)

func init() {
	for _, kind := range []string{"tunnel", "tun", "wireguard"} {
		localservice.Register(kind, newLocalService)
	}
}

type localBox struct {
	box          *box.Box
	spec         conf.LocalService
	tracker      *serviceTracker
	closed       bool
	wirePrevious map[string][2]int64
	finalTraffic []localservice.Traffic
}
type serviceTracker struct {
	mu          sync.Mutex
	wg          bool
	addresses   map[netip.Addr]int
	counters    map[int]*counter.TrafficStorage
	buckets     map[int]*ratelimit.Bucket
	connections map[int]map[*managedLease]struct{}
}

func prefixes(texts []string) []netip.Prefix {
	var result []netip.Prefix
	for _, s := range texts {
		p, _ := netip.ParsePrefix(s)
		result = append(result, p)
	}
	return result
}
func servicePeers(s conf.LocalService) []option.WireGuardPeer {
	var peers []option.WireGuardPeer
	for _, p := range s.WireGuard.Peers {
		peers = append(peers, option.WireGuardPeer{PublicKey: p.PublicKey, PreSharedKey: p.PreSharedKey, AllowedIPs: prefixes(p.Address), PersistentKeepaliveInterval: p.KeepAlive})
	}
	return peers
}
func newLocalService(s conf.LocalService, _ string) (localservice.Instance, error) {
	ctx := box.Context(context.Background(), include.InboundRegistry(), include.OutboundRegistry(), include.EndpointRegistry(), include.DNSTransportRegistry(), include.ServiceRegistry(), include.CertificateProviderRegistry())
	document := map[string]any{"log": map[string]any{"disabled": true}, "outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}}, "route": map[string]any{"final": "direct"}}
	switch s.Type {
	case "tunnel":
		host, port, _ := net.SplitHostPort(s.Listen)
		listenPort, _ := strconv.Atoi(port)
		target, targetPort, _ := net.SplitHostPort(s.Target)
		p, _ := strconv.Atoi(targetPort)
		document["inbounds"] = []any{map[string]any{"type": "direct", "tag": s.ID, "listen": host, "listen_port": listenPort, "network": strings.Split(s.Network, ","), "override_address": target, "override_port": p}}
	case "tun":
		t := s.TUN
		if t.OutboundInterface != "" {
			if _, err := net.InterfaceByName(t.OutboundInterface); err != nil {
				return nil, fmt.Errorf("TUN outbound interface is unavailable")
			}
		}
		document["route"] = map[string]any{"final": "direct", "auto_detect_interface": true}
		if t.OutboundInterface != "" {
			document["outbounds"] = []any{map[string]any{"type": "direct", "tag": "direct", "bind_interface": t.OutboundInterface}}
		}
		document["inbounds"] = []any{map[string]any{"type": "tun", "tag": s.ID, "interface_name": t.Interface, "address": t.Address, "mtu": t.MTU, "auto_route": t.AutoRoute, "route_address": t.RouteAddress, "route_exclude_address": t.RouteExcludeAddress, "dns_mode": "disabled"}}
	case "wireguard":
		key, err := localservice.ReadSecret(s.WireGuard.PrivateKeyFile)
		if err != nil {
			return nil, err
		}
		if !conf.ValidWGKey(key) {
			return nil, fmt.Errorf("invalid WireGuard private key file")
		}
		_, port, _ := net.SplitHostPort(s.Listen)
		p, _ := strconv.Atoi(port)
		// WireGuard's userspace device binds a UDP port on all interfaces.
		// Reject a misleading per-address bind instead of silently widening it.
		host, _, _ := net.SplitHostPort(s.Listen)
		if host != "0.0.0.0" && host != "::" {
			return nil, fmt.Errorf("userspace WireGuard Listen must use 0.0.0.0 or ::")
		}
		document["endpoints"] = []any{map[string]any{"type": "wireguard", "tag": s.ID, "address": s.WireGuard.Address, "private_key": key, "listen_port": p, "mtu": s.WireGuard.MTU, "peers": servicePeers(s), "disable_flow": true}}
	}
	data, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, data)
	if err != nil {
		return nil, err
	}
	b, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		return nil, fmt.Errorf("create %s service failed", s.Type)
	}
	tracker := &serviceTracker{wg: s.Type == "wireguard", addresses: map[netip.Addr]int{}, counters: map[int]*counter.TrafficStorage{}, buckets: map[int]*ratelimit.Bucket{}, connections: map[int]map[*managedLease]struct{}{}}
	tracker.configure(s)
	b.Router().AppendTracker(tracker)
	if err = b.Start(); err != nil {
		_ = b.Close()
		return nil, fmt.Errorf("start %s service: %w", s.Type, err)
	}
	return &localBox{box: b, spec: s, tracker: tracker}, nil
}
func (b *localBox) Close() error {
	if b.closed {
		return nil
	}
	var collectErr error
	if b.spec.Type == "wireguard" {
		b.finalTraffic, collectErr = b.wireTraffic()
	}
	b.closed = true
	return errors.Join(collectErr, b.box.Close())
}
func (b *localBox) Health() error {
	if b.closed {
		return fmt.Errorf("service is closed")
	}
	if b.spec.Type == "tun" {
		_, err := net.InterfaceByName(b.spec.TUN.Interface)
		return err
	}
	return nil
}
func (b *localBox) PeerStatus() ([]localservice.PeerStatus, error) {
	if b.spec.Type != "wireguard" {
		return nil, nil
	}
	ep, found := b.box.Endpoint().Get(b.spec.ID)
	if !found {
		return nil, fmt.Errorf("WireGuard endpoint not found")
	}
	stats, err := ep.(interface {
		PeerStats() ([]wgtransport.PeerStatistics, error)
	}).PeerStats()
	if err != nil {
		return nil, err
	}
	uids := map[string]int{}
	addresses := map[string][]string{}
	for _, p := range b.spec.WireGuard.Peers {
		uids[p.PublicKey] = p.UID
		addresses[p.PublicKey] = p.Address
	}
	var result []localservice.PeerStatus
	for _, p := range stats {
		result = append(result, localservice.PeerStatus{UID: uids[p.PublicKey], PublicKey: p.PublicKey, Address: addresses[p.PublicKey], Endpoint: p.Endpoint, LastHandshake: p.LastHandshake})
	}
	return result, nil
}
func (b *localBox) Traffic() ([]localservice.Traffic, error) {
	if b.spec.Type == "wireguard" {
		if b.closed {
			rows := b.finalTraffic
			b.finalTraffic = nil
			return rows, nil
		}
		return b.wireTraffic()
	}
	b.tracker.mu.Lock()
	defer b.tracker.mu.Unlock()
	var traffic []localservice.Traffic
	for uid, c := range b.tracker.counters {
		up, down := c.Snapshot(true)
		if up+down > 0 {
			traffic = append(traffic, localservice.Traffic{UID: uid, Upload: up, Download: down})
		}
	}
	return traffic, nil
}

// Native peer counters include every authenticated IP protocol and WireGuard
// overhead, independent of the proxy stack's TCP/UDP acceleration decisions.
func (b *localBox) wireTraffic() ([]localservice.Traffic, error) {
	ep, found := b.box.Endpoint().Get(b.spec.ID)
	if !found {
		return nil, fmt.Errorf("WireGuard endpoint not found")
	}
	stats, err := ep.(interface {
		PeerStats() ([]wgtransport.PeerStatistics, error)
	}).PeerStats()
	if err != nil {
		return nil, err
	}
	if b.wirePrevious == nil {
		b.wirePrevious = map[string][2]int64{}
	}
	uids := map[string]int{}
	for _, p := range b.spec.WireGuard.Peers {
		uids[p.PublicKey] = p.UID
	}
	var rows []localservice.Traffic
	for _, p := range stats {
		uid := uids[p.PublicKey]
		if uid == 0 {
			continue
		}
		old := b.wirePrevious[p.PublicKey]
		up, down := p.Received-old[0], p.Sent-old[1]
		if up < 0 {
			up = p.Received
		}
		if down < 0 {
			down = p.Sent
		}
		b.wirePrevious[p.PublicKey] = [2]int64{p.Received, p.Sent}
		if up+down > 0 {
			rows = append(rows, localservice.Traffic{UID: uid, Upload: up, Download: down})
		}
	}
	return rows, nil
}
func (b *localBox) Update(s conf.LocalService) error {
	if b.spec.Type != "wireguard" || s.Type != "wireguard" {
		return localservice.ErrRestartRequired
	}
	oldSpec, newSpec := b.spec, s
	oldWG, newWG := *oldSpec.WireGuard, *newSpec.WireGuard
	oldWG.Peers, newWG.Peers = nil, nil
	oldSpec.WireGuard, newSpec.WireGuard = &oldWG, &newWG
	if !reflect.DeepEqual(oldSpec, newSpec) {
		return localservice.ErrRestartRequired
	}
	ep, found := b.box.Endpoint().Get(s.ID)
	if !found {
		return fmt.Errorf("WireGuard endpoint not found")
	}
	updater, ok := ep.(interface {
		UpdatePeers([]option.WireGuardPeer) error
	})
	if !ok {
		return fmt.Errorf("WireGuard peer management unavailable")
	}
	// Revoke affected source identities before changing their key/address owner.
	b.tracker.revokeChanged(b.spec, s)
	if err := updater.UpdatePeers(servicePeers(s)); err != nil {
		if restoreErr := updater.UpdatePeers(servicePeers(b.spec)); restoreErr != nil {
			return fmt.Errorf("WireGuard peer update and rollback failed")
		}
		b.tracker.configure(b.spec)
		return err
	}
	b.tracker.configure(s)
	retained := map[string]bool{}
	for _, p := range s.WireGuard.Peers {
		retained[p.PublicKey] = true
	}
	for key := range b.wirePrevious {
		if !retained[key] {
			delete(b.wirePrevious, key)
		}
	}
	b.spec = s
	return nil
}
func (t *serviceTracker) configure(s conf.LocalService) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.addresses = map[netip.Addr]int{}
	if s.WireGuard != nil {
		for _, p := range s.WireGuard.Peers {
			for _, a := range p.Address {
				prefix, _ := netip.ParsePrefix(a)
				t.addresses[prefix.Addr()] = p.UID
			}
			if t.counters[p.UID] == nil {
				t.counters[p.UID] = &counter.TrafficStorage{}
			}
			if p.SpeedLimit > 0 {
				t.buckets[p.UID] = ratelimit.NewBucketWithRate(float64(p.SpeedLimit)*125000, int64(p.SpeedLimit)*125000)
			} else {
				delete(t.buckets, p.UID)
			}
		}
	}
	if !t.wg && t.counters[0] == nil {
		t.counters[0] = &counter.TrafficStorage{}
	}
}
func (t *serviceTracker) revokeChanged(old, next conf.LocalService) {
	newPeers := map[int]conf.WireGuardPeer{}
	for _, p := range next.WireGuard.Peers {
		newPeers[p.UID] = p
	}
	t.mu.Lock()
	var closeList []*managedLease
	for _, p := range old.WireGuard.Peers {
		n, ok := newPeers[p.UID]
		if !ok || p.PublicKey != n.PublicKey || p.PreSharedKey != n.PreSharedKey || !reflect.DeepEqual(p.Address, n.Address) {
			for _, a := range p.Address {
				prefix, _ := netip.ParsePrefix(a)
				delete(t.addresses, prefix.Addr())
			}
			for c := range t.connections[p.UID] {
				closeList = append(closeList, c)
			}
		}
	}
	t.mu.Unlock()
	for _, c := range closeList {
		c.Close()
	}
}
func (t *serviceTracker) lease(source netip.Addr, c io.Closer) (int, *counter.TrafficStorage, *managedLease, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	uid := 0
	if t.wg {
		var ok bool
		uid, ok = t.addresses[source]
		if !ok {
			c.Close()
			return 0, nil, nil, false
		}
	}
	storage := t.counters[uid]
	if storage == nil {
		storage = &counter.TrafficStorage{}
		t.counters[uid] = storage
	}
	if t.connections[uid] == nil {
		t.connections[uid] = map[*managedLease]struct{}{}
	}
	l := &managedLease{Closer: c}
	l.release = func() { t.mu.Lock(); delete(t.connections[uid], l); t.mu.Unlock() }
	t.connections[uid][l] = struct{}{}
	return uid, storage, l, true
}
func (t *serviceTracker) bucket(uid int) (*ratelimit.Bucket, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buckets[uid], false
}
func (t *serviceTracker) RoutedConnection(_ context.Context, c net.Conn, m adapter.InboundContext, _ adapter.Rule, _ adapter.Outbound) net.Conn {
	uid, storage, l, ok := t.lease(m.Source.Addr, c)
	if !ok {
		return c
	}
	c = &managedConn{Conn: c, lease: l}
	if t.wg {
		c = rate.NewDynamicConnRateLimiter(c, func() (*ratelimit.Bucket, bool) { return t.bucket(uid) })
	}
	return counter.NewConnCounter(c, storage)
}
func (t *serviceTracker) RoutedPacketConnection(_ context.Context, c N.PacketConn, m adapter.InboundContext, _ adapter.Rule, _ adapter.Outbound) N.PacketConn {
	uid, storage, l, ok := t.lease(m.Source.Addr, c)
	if !ok {
		return c
	}
	c = &managedPacket{PacketConn: c, lease: l}
	if t.wg {
		c = rate.NewPacketRateLimiter(c, func() (*ratelimit.Bucket, bool) { return t.bucket(uid) })
	}
	return counter.NewPacketConnCounter(c, storage)
}

type serviceFlow struct{ storage *counter.TrafficStorage }

func (f *serviceFlow) AttachFlow(tun.FlowHandle)     {}
func (f *serviceFlow) CountForward(n int)            { f.storage.UpCounter.Add(int64(n)) }
func (f *serviceFlow) CountReverse(n int)            { f.storage.DownCounter.Add(int64(n)) }
func (f *serviceFlow) FlowEstablished()              {}
func (f *serviceFlow) CloseFlow(tun.FlowCloseReason) {}
func (t *serviceTracker) RoutedFlow(context.Context, adapter.InboundContext, adapter.Rule, adapter.Outbound) tun.FlowTracker {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.counters[0] == nil {
		t.counters[0] = &counter.TrafficStorage{}
	}
	return &serviceFlow{storage: t.counters[0]}
}
