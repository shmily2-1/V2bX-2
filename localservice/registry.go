package localservice

import (
	"fmt"
	"github.com/shmily2-1/V2bX-2/conf"
)

type Traffic struct {
	UID      int `json:"UID,omitempty"`
	Upload   int64
	Download int64
}
type Instance interface {
	Close() error
	Health() error
	Traffic() ([]Traffic, error) // Atomic deltas; never cumulative totals.
}
type LiveUpdater interface{ Update(conf.LocalService) error }
type PeerStatus struct {
	UID           int
	PublicKey     string
	Address       []string
	Endpoint      string
	LastHandshake int64
}
type PeerObserver interface{ PeerStatus() ([]PeerStatus, error) }
type ProcessStatus struct {
	PID      int
	ExitCode *int `json:"ExitCode,omitempty"`
}
type ProcessObserver interface{ ProcessStatus() ProcessStatus }
type Factory func(conf.LocalService, string) (Instance, error)

var factories = map[string]Factory{}

func Register(kind string, factory Factory) { factories[kind] = factory }
func Open(spec conf.LocalService, stateDir string) (Instance, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	f := factories[spec.Type]
	if f == nil {
		return nil, fmt.Errorf("service backend %s is not included in this build", spec.Type)
	}
	return f(spec, stateDir)
}
