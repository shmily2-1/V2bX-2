package xray

import (
	"context"
	"fmt"
	"strings"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/common/counter"
	"github.com/shmily2-1/V2bX-2/common/format"
	vCore "github.com/shmily2-1/V2bX-2/core"
	"github.com/shmily2-1/V2bX-2/core/xray/app/dispatcher"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy"
	"github.com/xtls/xray-core/proxy/masque"
)

func (c *Xray) GetUserManager(tag string) (proxy.UserManager, error) {
	handler, err := c.ihm.GetHandler(context.Background(), tag)
	if err != nil {
		return nil, fmt.Errorf("no such inbound tag: %s", err)
	}
	inboundInstance, ok := handler.(proxy.GetInbound)
	if !ok {
		return nil, fmt.Errorf("handler %s is not implement proxy.GetInbound", tag)
	}
	userManager, ok := inboundInstance.GetInbound().(proxy.UserManager)
	if !ok {
		return nil, fmt.Errorf("handler %s is not implement proxy.UserManager", tag)
	}
	return userManager, nil
}

func (c *Xray) DelUsers(users []panel.UserInfo, tag string, info *panel.NodeInfo) error {
	userManager, err := c.GetUserManager(tag)
	if err != nil {
		return fmt.Errorf("get user manager error: %s", err)
	}
	var user string
	c.users.mapLock.Lock()
	defer c.users.mapLock.Unlock()
	for i := range users {
		user = format.UserTag(tag, users[i].Uuid)
		c.dispatcher.AuthorizedUsers.Delete(user)
		name := user
		if info.Type == "masque" {
			name = users[i].Uuid
		}
		err = userManager.RemoveUser(context.Background(), name)
		if err != nil {
			return err
		}
		delete(c.users.uidMap, user)
		delete(c.users.tags[tag], user)
		if v, ok := c.dispatcher.Counter.Load(tag); ok {
			tc := v.(*counter.TrafficCounter)
			tc.Delete(user)
		}
		if v, ok := c.dispatcher.LinkManagers.Load(user); ok {
			lm := v.(*dispatcher.LinkManager)
			lm.CloseAll()
			c.dispatcher.LinkManagers.Delete(user)
		}
	}
	return nil
}

func (x *Xray) GetUserTrafficSlice(tag string, reset bool) ([]panel.UserTraffic, error) {
	trafficSlice := make([]panel.UserTraffic, 0)
	x.users.mapLock.RLock()
	defer x.users.mapLock.RUnlock()
	if v, ok := x.dispatcher.Counter.Load(tag); ok {
		c := v.(*counter.TrafficCounter)
		c.Counters.Range(func(key, value interface{}) bool {
			email := key.(string)
			traffic := value.(*counter.TrafficStorage)
			up := traffic.UpCounter.Load()
			down := traffic.DownCounter.Load()
			if up+down > x.nodeReportMinTrafficBytes[tag] || (reset && up+down > 0) {
				up, down = traffic.Snapshot(reset)
				if x.users.uidMap[email] == 0 {
					c.Delete(email)
					return true
				}
				trafficSlice = append(trafficSlice, panel.UserTraffic{
					UID:      x.users.uidMap[email],
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

func (c *Xray) AddUsers(p *vCore.AddUsersParams) (added int, err error) {
	c.users.mapLock.Lock()
	defer c.users.mapLock.Unlock()
	var users []*protocol.User
	switch p.NodeInfo.Type {
	case "masque":
		seen := map[string]bool{}
		uids := map[int]bool{}
		for _, u := range p.Users {
			if u.DeviceLimit != 0 {
				return 0, fmt.Errorf("MASQUE device limits require external endpoint attribution, which is not available in this Xray adapter")
			}
			if u.Id <= 0 || u.Uuid == "" || strings.ContainsAny(u.Uuid, ":|\r\n\x00") || u.Password == "" || seen[strings.ToLower(u.Uuid)] || uids[u.Id] {
				return 0, fmt.Errorf("MASQUE requires a nonempty username without colon and an explicit password")
			}
			seen[strings.ToLower(u.Uuid)], uids[u.Id] = true, true
			users = append(users, &protocol.User{Email: u.Uuid, Account: serial.ToTypedMessage(&masque.Account{Password: u.Password})})
		}
	case "vmess":
		users = buildVmessUsers(p.Tag, p.Users)
	case "vless":
		users = buildVlessUsers(p.Tag, p.Users, p.VAllss.Flow)
	case "trojan":
		users = buildTrojanUsers(p.Tag, p.Users)
	case "shadowsocks":
		users = buildSSUsers(p.Tag,
			p.Users,
			p.Shadowsocks.Cipher,
			p.Shadowsocks.ServerKey)
	default:
		return 0, fmt.Errorf("unsupported node type: %s", p.NodeInfo.Type)
	}
	man, err := c.GetUserManager(p.Tag)
	if err != nil {
		return 0, fmt.Errorf("get user manager error: %s", err)
	}
	for i, u := range users {
		mUser, err := u.ToMemoryUser()
		if err != nil {
			return 0, err
		}
		err = man.AddUser(context.Background(), mUser)
		if err != nil {
			return 0, err
		}
		identity := format.UserTag(p.Tag, p.Users[i].Uuid)
		c.users.uidMap[identity] = p.Users[i].Id
		if c.users.tags[p.Tag] == nil {
			c.users.tags[p.Tag] = map[string]struct{}{}
		}
		c.users.tags[p.Tag][identity] = struct{}{}
		c.dispatcher.AuthorizedUsers.Store(identity, mUser.Account)
	}
	return len(users), nil
}

func (c *Xray) revokeNode(tag string) {
	c.users.mapLock.Lock()
	defer c.users.mapLock.Unlock()
	for identity := range c.users.tags[tag] {
		c.dispatcher.AuthorizedUsers.Delete(identity)
		if value, ok := c.dispatcher.LinkManagers.LoadAndDelete(identity); ok {
			value.(*dispatcher.LinkManager).CloseAll()
		}
		delete(c.users.uidMap, identity)
	}
	delete(c.users.tags, tag)
	c.dispatcher.Counter.Delete(tag)
}
