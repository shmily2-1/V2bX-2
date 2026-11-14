package wireguard

import (
	"github.com/sagernet/sing-box/option"
	transport "github.com/sagernet/sing-box/transport/wireguard"
	M "github.com/sagernet/sing/common/metadata"
)

func (w *Endpoint) UpdatePeers(peers []option.WireGuardPeer) error {
	options := make([]transport.PeerOptions, 0, len(peers))
	for _, p := range peers {
		options = append(options, transport.PeerOptions{Endpoint: M.ParseSocksaddrHostPort(p.Address, p.Port), PublicKey: p.PublicKey, PreSharedKey: p.PreSharedKey, AllowedIPs: p.AllowedIPs, PersistentKeepaliveInterval: p.PersistentKeepaliveInterval})
	}
	return w.endpoint.ApplyPeers(options)
}
func (w *Endpoint) PeerStats() ([]transport.PeerStatistics, error) { return w.endpoint.PeerStats() }
