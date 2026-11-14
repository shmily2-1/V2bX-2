package sing

import (
	"context"
	"fmt"
	"github.com/juju/ratelimit"
	"net"
	"sync"

	"github.com/shmily2-1/V2bX-2/common/format"
	"github.com/shmily2-1/V2bX-2/common/rate"

	"github.com/shmily2-1/V2bX-2/limiter"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	tun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/auth"
	N "github.com/sagernet/sing/common/network"
	"github.com/shmily2-1/V2bX-2/common/counter"
)

var _ adapter.ConnectionTracker = (*HookServer)(nil)

type HookServer struct {
	managedMu   sync.Mutex
	managed     map[string]map[string]*managedUser
	managedAuth map[string]*auth.Authenticator
	counter     sync.Map //map[string]*counter.TrafficCounter
}

// V2bX creates proxy inbounds, not TUN inbounds. TUN fast-path flows have
// no panel user identity and are deliberately not billed as proxy users.
func (h *HookServer) RoutedFlow(context.Context, adapter.InboundContext, adapter.Rule, adapter.Outbound) tun.FlowTracker {
	return nil
}

func (h *HookServer) ModeList() []string {
	return nil
}

func (h *HookServer) RoutedConnection(ctx context.Context, conn net.Conn, m adapter.InboundContext, _ adapter.Rule, _ adapter.Outbound) net.Conn {
	lease, allowed := h.lease(ctx, m.Inbound, m.User, conn)
	if !allowed {
		return conn
	}
	if lease != nil {
		conn = &managedConn{Conn: conn, lease: lease}
	}
	l, err := limiter.GetLimiter(m.Inbound)
	if err != nil {
		log.Warn("get limiter for ", m.Inbound, " error: ", err)
		return conn
	}
	taguuid := format.UserTag(m.Inbound, m.User)
	ip := m.Source.Addr.String()
	if b, r := l.CheckLimit(taguuid, ip, true, true); r {
		conn.Close()
		log.Error("[", m.Inbound, "] ", "Limited ", m.User, " by ip or conn")
		return conn
	} else if b != nil && !l.DynamicEnabled() {
		conn = rate.NewConnRateLimiter(conn, b)
	}
	if l.DynamicEnabled() {
		conn = rate.NewDynamicConnRateLimiter(conn, func() (*ratelimit.Bucket, bool) { return l.CurrentBucket(taguuid) })
	}
	if l != nil {
		destStr := m.Destination.AddrString()
		protocol := m.Protocol
		if l.CheckDomainRule(destStr) {
			log.Error(fmt.Sprintf(
				"User %s access domain %s reject by rule",
				m.User,
				destStr))
			conn.Close()
			return conn
		}
		if len(protocol) != 0 {
			if l.CheckProtocolRule(protocol) {
				log.Error(fmt.Sprintf(
					"User %s access protocol %s reject by rule",
					m.User,
					protocol))
				conn.Close()
				return conn
			}
		}
	}
	var t *counter.TrafficCounter
	if c, ok := h.counter.Load(m.Inbound); !ok {
		actual, _ := h.counter.LoadOrStore(m.Inbound, counter.NewTrafficCounter())
		t = actual.(*counter.TrafficCounter)
	} else {
		t = c.(*counter.TrafficCounter)
	}
	conn = counter.NewConnCounter(conn, t.GetCounter(m.User))
	return conn
}

func (h *HookServer) RoutedPacketConnection(ctx context.Context, conn N.PacketConn, m adapter.InboundContext, _ adapter.Rule, _ adapter.Outbound) N.PacketConn {
	lease, allowed := h.lease(ctx, m.Inbound, m.User, conn)
	if !allowed {
		return conn
	}
	if lease != nil {
		conn = &managedPacket{PacketConn: conn, lease: lease}
	}
	l, err := limiter.GetLimiter(m.Inbound)
	if err != nil {
		log.Warn("get limiter for ", m.Inbound, " error: ", err)
		return conn
	}
	ip := m.Source.Addr.String()
	taguuid := format.UserTag(m.Inbound, m.User)
	if b, r := l.CheckLimit(taguuid, ip, false, false); r {
		conn.Close()
		log.Error("[", m.Inbound, "] ", "Limited ", m.User, " by ip or conn")
		return conn
	} else if b != nil && !l.DynamicEnabled() {
		conn = rate.NewPacketRateLimiter(conn, func() (*ratelimit.Bucket, bool) { return b, false })
	}
	if l.DynamicEnabled() {
		conn = rate.NewPacketRateLimiter(conn, func() (*ratelimit.Bucket, bool) { return l.CurrentBucket(taguuid) })
	}
	if l != nil {
		destStr := m.Destination.AddrString()
		protocol := m.Destination.Network()
		if l.CheckDomainRule(destStr) {
			log.Error(fmt.Sprintf(
				"User %s access domain %s reject by rule",
				m.User,
				destStr))
			conn.Close()
			return conn
		}
		if len(protocol) != 0 {
			if l.CheckProtocolRule(protocol) {
				log.Error(fmt.Sprintf(
					"User %s access protocol %s reject by rule",
					m.User,
					protocol))
				conn.Close()
				return conn
			}
		}
	}
	var t *counter.TrafficCounter
	if c, ok := h.counter.Load(m.Inbound); !ok {
		actual, _ := h.counter.LoadOrStore(m.Inbound, counter.NewTrafficCounter())
		t = actual.(*counter.TrafficCounter)
	} else {
		t = c.(*counter.TrafficCounter)
	}
	conn = counter.NewPacketConnCounter(conn, t.GetCounter(m.User))
	return conn
}
