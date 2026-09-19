package hy2

import (
	"errors"
	"sync"

	"github.com/shmily2-1/V2bX-2/conf"
	vCore "github.com/shmily2-1/V2bX-2/core"
	"go.uber.org/zap"
)

var _ vCore.Core = (*Hysteria2)(nil)

type Hysteria2 struct {
	nodesMu  sync.RWMutex
	closed   bool
	Hy2nodes map[string]Hysteria2node
	Auth     *V2bX
	Logger   *zap.Logger
}

func init() {
	vCore.RegisterCore("hysteria2", New)
}

func New(c *conf.CoreConfig) (vCore.Core, error) {
	loglever := "error"
	if c.Hysteria2Config.LogConfig.Level != "" {
		loglever = c.Hysteria2Config.LogConfig.Level
	}
	log, err := initLogger(loglever, "console")
	if err != nil {
		return nil, err
	}
	return &Hysteria2{
		Hy2nodes: make(map[string]Hysteria2node),
		Auth: &V2bX{
			usersMap: make(map[string]int),
		},
		Logger: log,
	}, nil
}

func (h *Hysteria2) Protocols() []string {
	return []string{
		"hysteria2",
	}
}

func (h *Hysteria2) Start() error {
	return nil
}

func (h *Hysteria2) Close() error {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	h.closed = true
	var errs []error
	for tag := range h.Hy2nodes {
		errs = append(errs, h.closeNode(tag))
	}
	return errors.Join(errs...)
}

func (h *Hysteria2) Type() string {
	return "hysteria2"
}
