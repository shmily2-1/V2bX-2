package hy2

import (
	"fmt"
	"net"
	"sync"

	"github.com/apernet/hysteria/core/v2/server"
	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/common/counter"
	vCore "github.com/shmily2-1/V2bX-2/core"
)

var _ server.Authenticator = &V2bX{}

type V2bX struct {
	usersMap map[string]int
	mutex    sync.RWMutex
}

func (v *V2bX) Authenticate(addr net.Addr, auth string, tx uint64) (ok bool, id string) {
	v.mutex.RLock()
	defer v.mutex.RUnlock()
	if _, exists := v.usersMap[auth]; exists {
		return true, auth
	}
	return false, ""
}

func (h *Hysteria2) AddUsers(p *vCore.AddUsersParams) (added int, err error) {
	var wg sync.WaitGroup
	for _, user := range p.Users {
		wg.Add(1)
		go func(u panel.UserInfo) {
			defer wg.Done()
			h.Auth.mutex.Lock()
			h.Auth.usersMap[u.Uuid] = u.Id
			h.Auth.mutex.Unlock()
		}(user)
	}
	wg.Wait()
	return len(p.Users), nil
}

func (h *Hysteria2) DelUsers(users []panel.UserInfo, tag string, _ *panel.NodeInfo) error {
	h.nodesMu.RLock()
	n, exists := h.Hy2nodes[tag]
	h.nodesMu.RUnlock()
	if !exists {
		return fmt.Errorf("node %q not found", tag)
	}
	var wg sync.WaitGroup
	for _, user := range users {
		wg.Add(1)
		if v, ok := n.TrafficLogger.(*HookServer).Counter.Load(tag); ok {
			c := v.(*counter.TrafficCounter)
			c.Delete(user.Uuid)
		}
		go func(u panel.UserInfo) {
			defer wg.Done()
			h.Auth.mutex.Lock()
			delete(h.Auth.usersMap, u.Uuid)
			h.Auth.mutex.Unlock()
		}(user)
	}
	wg.Wait()
	return nil
}

func (h *Hysteria2) GetUserTrafficSlice(tag string, reset bool) ([]panel.UserTraffic, error) {
	h.nodesMu.RLock()
	n, exists := h.Hy2nodes[tag]
	h.nodesMu.RUnlock()
	if !exists {
		return nil, nil
	}
	trafficSlice := make([]panel.UserTraffic, 0)
	h.Auth.mutex.RLock()
	defer h.Auth.mutex.RUnlock()
	hook := n.TrafficLogger.(*HookServer)
	if v, ok := hook.Counter.Load(tag); ok {
		c := v.(*counter.TrafficCounter)
		c.Counters.Range(func(key, value interface{}) bool {
			uuid := key.(string)
			traffic := value.(*counter.TrafficStorage)
			up := traffic.UpCounter.Load()
			down := traffic.DownCounter.Load()
			if up+down > hook.ReportMinTrafficBytes {
				if reset {
					traffic.UpCounter.Store(0)
					traffic.DownCounter.Store(0)
				}
				if h.Auth.usersMap[uuid] == 0 {
					c.Delete(uuid)
					return true
				}
				trafficSlice = append(trafficSlice, panel.UserTraffic{
					UID:      h.Auth.usersMap[uuid],
					Upload:   up,
					Download: down,
				})
			}
			return true
		})
		if len(trafficSlice) == 0 {
			return nil, nil
		}
		return trafficSlice, nil
	}
	return nil, nil
}
