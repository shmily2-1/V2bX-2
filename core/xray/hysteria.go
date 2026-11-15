package xray

import (
	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/common/format"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/hysteria/account"
)

func buildHysteriaUsers(tag string, users []panel.UserInfo) []*protocol.User {
	result := make([]*protocol.User, len(users))
	for i := range users {
		result[i] = &protocol.User{
			Level:   0,
			Email:   format.UserTag(tag, users[i].Uuid),
			Account: serial.ToTypedMessage(&account.Account{Auth: users[i].Uuid}),
		}
	}
	return result
}
