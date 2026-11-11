package anytls

import (
	"io"

	"github.com/sagernet/sing-box/option"
)

func (h *Inbound) AddUsers(users []option.AnyTLSUser) error {
	for _, user := range users {
		h.uuidlist = append(h.uuidlist, user.Name)
	}
	passwords := append([]string(nil), h.uuidlist...)
	return h.service.UpdateUsers(h.uuidlist, passwords)
}

func (h *Inbound) DelUsers(names []string) error {
	if len(names) == 0 {
		return nil
	}

	toDelete := make(map[string]struct{})
	for _, name := range names {
		toDelete[name] = struct{}{}
		h.userconns.Range(func(key, value interface{}) bool {
			if value.(string) == name {
				key.(io.Closer).Close()
				h.userconns.Delete(key)
			}
			return true
		})
	}

	remaining := make([]string, 0, len(h.uuidlist))
	for _, uuid := range h.uuidlist {
		if _, found := toDelete[uuid]; !found {
			remaining = append(remaining, uuid)
		}
	}

	h.uuidlist = remaining
	passwords := append([]string(nil), h.uuidlist...)
	return h.service.UpdateUsers(h.uuidlist, passwords)
}
