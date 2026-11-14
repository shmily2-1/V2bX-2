package sing

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	transporthttp "github.com/sagernet/sing-box/transport/http"
	"github.com/sagernet/sing/common/auth"
	N "github.com/sagernet/sing/common/network"
	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/common/counter"
	"github.com/shmily2-1/V2bX-2/common/format"
)

type managedUser struct {
	active      bool
	connections map[*managedLease]struct{}
}
type managedLease struct {
	io.Closer
	once    sync.Once
	release func()
}

func (l *managedLease) Close() error {
	err := l.Closer.Close()
	l.once.Do(l.release)
	return err
}

type managedConn struct {
	net.Conn
	lease *managedLease
}

func (c *managedConn) Close() error { return c.lease.Close() }

type managedPacket struct {
	N.PacketConn
	lease *managedLease
}

func (c *managedPacket) Close() error { return c.lease.Close() }

// Preserve packet buffer requirements without exposing an unwrapping path
// that could bypass the connection's authorization/limiting wrappers.
func (c *managedPacket) FrontHeadroom() int { return N.CalculateFrontHeadroom(c.PacketConn) }
func (c *managedPacket) RearHeadroom() int  { return N.CalculateRearHeadroom(c.PacketConn) }

func (h *HookServer) setManaged(tag, user string, active bool) {
	h.managedMu.Lock()
	defer h.managedMu.Unlock()
	if h.managed == nil {
		h.managed = make(map[string]map[string]*managedUser)
	}
	if h.managed[tag] == nil {
		h.managed[tag] = make(map[string]*managedUser)
	}
	if user != "" {
		h.managed[tag][user] = &managedUser{active: active, connections: make(map[*managedLease]struct{})}
	}
}

func (h *HookServer) setManagedAuth(tag string, value *auth.Authenticator) {
	h.managedMu.Lock()
	defer h.managedMu.Unlock()
	if h.managedAuth == nil {
		h.managedAuth = map[string]*auth.Authenticator{}
	}
	h.managedAuth[tag] = value
}
func (h *HookServer) lease(ctx context.Context, tag, user string, connection io.Closer) (*managedLease, bool) {
	h.managedMu.Lock()
	defer h.managedMu.Unlock()
	users, managed := h.managed[tag]
	if !managed {
		return nil, true
	}
	u := users[user]
	if u == nil || !u.active || h.managedAuth[tag] != transporthttp.AuthenticatorFromContext(ctx) {
		connection.Close()
		return nil, false
	}
	l := &managedLease{Closer: connection}
	l.release = func() { h.managedMu.Lock(); delete(u.connections, l); h.managedMu.Unlock() }
	u.connections[l] = struct{}{}
	return l, true
}

func (h *HookServer) revokeManaged(tag, user string) {
	h.managedMu.Lock()
	u := h.managed[tag][user]
	var connections []*managedLease
	if u != nil {
		u.active = false
		for c := range u.connections {
			connections = append(connections, c)
		}
		delete(h.managed[tag], user)
	}
	h.managedMu.Unlock()
	for _, c := range connections {
		c.Close()
	}
}
func (h *HookServer) closeManaged(tag string) {
	h.managedMu.Lock()
	var users []string
	for user := range h.managed[tag] {
		users = append(users, user)
	}
	h.managedMu.Unlock()
	for _, user := range users {
		h.revokeManaged(tag, user)
	}
	// Keep a deny-all tombstone until the inbound has stopped accepting traffic.
}

func (b *Sing) updateHTTPUsers(tag string, add, remove []panel.UserInfo) error {
	b.nodesMu.Lock()
	defer b.nodesMu.Unlock()
	in, found := b.box.Inbound().Get(tag)
	users, managed := b.httpUsers[tag]
	if !found || !managed {
		return fmt.Errorf("managed HTTP inbound not found")
	}
	names := map[string]bool{}
	uids := map[int]string{}
	for name, u := range users {
		names[name] = true
		uids[u.Id] = name
	}
	for _, u := range add {
		if u.Id <= 0 || u.Uuid == "" || u.Password == "" || strings.ContainsAny(u.Uuid, ":|\r\n\x00") || len(u.Uuid) > 255 || len(u.Password) > 255 {
			return fmt.Errorf("HTTP/Mixed requires positive id, uuid as username (1..255 bytes, no colon), and explicit password (1..255 bytes)")
		}
		if names[u.Uuid] || uids[u.Id] != "" {
			return fmt.Errorf("HTTP user already exists")
		}
		names[u.Uuid], uids[u.Id] = true, u.Uuid
	}
	b.users.mapLock.Lock()
	defer b.users.mapLock.Unlock()
	for _, u := range remove {
		delete(users, u.Uuid)
		b.hookServer.revokeManaged(tag, u.Uuid)
		delete(b.users.uidMap, format.UserTag(tag, u.Uuid))
		if value, ok := b.hookServer.counter.Load(tag); ok {
			value.(*counter.TrafficCounter).Delete(u.Uuid)
		}
	}
	for _, u := range add {
		users[u.Uuid] = u
		b.users.uidMap[format.UserTag(tag, u.Uuid)] = u.Id
		b.hookServer.setManaged(tag, u.Uuid, true)
	}
	authUsers := make([]auth.User, 0, len(users))
	for _, u := range users {
		authUsers = append(authUsers, auth.User{Username: u.Uuid, Password: u.Password})
	}
	b.hookServer.setManagedAuth(tag, in.(interface {
		SetUsers([]auth.User) *auth.Authenticator
	}).SetUsers(authUsers))
	return nil
}
