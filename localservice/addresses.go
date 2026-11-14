package localservice

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"strconv"

	"github.com/shmily2-1/V2bX-2/conf"
)

func validatePools(specs []conf.LocalService) error {
	var pools []netip.Prefix
	for _, s := range specs {
		if s.WireGuard == nil {
			continue
		}
		for _, text := range s.WireGuard.Address {
			p, _ := netip.ParsePrefix(text)
			for _, other := range pools {
				if p.Overlaps(other) {
					return fmt.Errorf("WireGuard service pools overlap")
				}
			}
			pools = append(pools, p)
		}
	}
	return nil
}

// Allocate preserves UID addresses across key rotation and process restart.
// Only exact host routes are accepted from peers, preventing source spoofing.
func Allocate(s conf.LocalService, path string) (conf.LocalService, map[string][]string, error) {
	if s.WireGuard == nil {
		return s, nil, nil
	}
	previous := map[string][]string{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(data, &previous); err != nil {
			return s, nil, fmt.Errorf("invalid allocation state")
		}
	} else if !os.IsNotExist(err) {
		return s, nil, err
	}
	w := *s.WireGuard
	w.Peers = append([]conf.WireGuardPeer{}, w.Peers...)
	s.WireGuard = &w
	used := map[netip.Addr]bool{}
	reserved := map[netip.Addr]int{}
	for _, text := range w.Address {
		p, _ := netip.ParsePrefix(text)
		used[p.Addr()] = true
		used[p.Masked().Addr()] = true
	}
	for _, peer := range w.Peers {
		for _, text := range peer.Address {
			p, _ := netip.ParsePrefix(text)
			used[p.Addr()] = true
		}
	}
	// Reserve every surviving UID's previous addresses before assigning new
	// peers, so reordering a list cannot steal an existing allocation.
	for _, peer := range w.Peers {
		if len(peer.Address) > 0 {
			continue
		}
		for _, text := range previous[strconv.Itoa(peer.UID)] {
			p, err := netip.ParsePrefix(text)
			if err != nil || p.Bits() != p.Addr().BitLen() {
				continue
			}
			for _, poolText := range w.Address {
				pool, _ := netip.ParsePrefix(poolText)
				if pool.Contains(p.Addr()) && !isBroadcast(pool, p.Addr()) && !used[p.Addr()] {
					used[p.Addr()] = true
					reserved[p.Addr()] = peer.UID
				}
			}
		}
	}
	assigned := map[string][]string{}
	for i := range w.Peers {
		u := &w.Peers[i]
		id := strconv.Itoa(u.UID)
		if len(u.Address) == 0 {
			for _, poolText := range w.Address {
				pool, _ := netip.ParsePrefix(poolText)
				var address netip.Addr
				for _, text := range previous[id] {
					p, err := netip.ParsePrefix(text)
					if err == nil && pool.Contains(p.Addr()) && reserved[p.Addr()] == u.UID {
						address = p.Addr()
						break
					}
				}
				if !address.IsValid() {
					for candidate, attempt := pool.Masked().Addr().Next(), 0; pool.Contains(candidate) && attempt < 65536; candidate, attempt = candidate.Next(), attempt+1 {
						if !used[candidate] && !isBroadcast(pool, candidate) {
							address = candidate
							break
						}
					}
				}
				if !address.IsValid() {
					return s, nil, fmt.Errorf("WireGuard address pool exhausted")
				}
				used[address] = true
				u.Address = append(u.Address, netip.PrefixFrom(address, address.BitLen()).String())
			}
		}
		assigned[id] = append([]string{}, u.Address...)
	}
	return s, assigned, s.Validate()
}
func isBroadcast(pool netip.Prefix, a netip.Addr) bool { return a.Is4() && !pool.Contains(a.Next()) }
