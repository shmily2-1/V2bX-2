package inbound

import (
	"context"
	"os"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/taskmonitor"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
)

var _ adapter.InboundManager = (*Manager)(nil)

type Manager struct {
	logger        log.ContextLogger
	registry      adapter.InboundRegistry
	endpoint      adapter.EndpointManager
	access        sync.Mutex
	inbounds      []adapter.Inbound
	inboundByTag  map[string]adapter.Inbound
	started       bool
	stage         adapter.StartStage
	scope         *adapter.Scope
	inboundScopes map[adapter.Inbound]*adapter.Scope
}

func NewManager(logger log.ContextLogger, registry adapter.InboundRegistry, endpoint adapter.EndpointManager) *Manager {
	return &Manager{
		logger:        logger,
		registry:      registry,
		endpoint:      endpoint,
		inboundByTag:  make(map[string]adapter.Inbound),
		inboundScopes: make(map[adapter.Inbound]*adapter.Scope),
	}
}

func (m *Manager) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	m.access.Lock()
	if m.started && m.stage >= stage {
		m.access.Unlock()
		panic("already started")
	}
	inbounds := m.inbounds
	if m.scope == nil {
		m.scope = scope
		scope.Add(m.close)
	}
	m.started = true
	m.stage = stage
	m.access.Unlock()
	for _, inbound := range inbounds {
		name := "inbound/" + inbound.Type() + "[" + inbound.Tag() + "]"
		m.access.Lock()
		inboundScope := m.inboundScopes[inbound]
		if inboundScope == nil {
			inboundScope = adapter.NewScope(scope.Context(), m.logger)
			m.inboundScopes[inbound] = inboundScope
		}
		m.access.Unlock()
		err := inboundScope.Start(name, inbound, stage)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Inbounds() []adapter.Inbound {
	m.access.Lock()
	defer m.access.Unlock()
	return m.inbounds
}

func (m *Manager) Get(tag string) (adapter.Inbound, bool) {
	m.access.Lock()
	inbound, found := m.inboundByTag[tag]
	m.access.Unlock()
	if found {
		return inbound, true
	}
	return m.endpoint.Get(tag)
}

func (m *Manager) Remove(tag string) error {
	m.access.Lock()
	inbound, found := m.inboundByTag[tag]
	if !found {
		m.access.Unlock()
		return os.ErrInvalid
	}
	delete(m.inboundByTag, tag)
	index := -1
	for i, item := range m.inbounds {
		if item == inbound {
			index = i
			break
		}
	}
	if index < 0 {
		m.access.Unlock()
		return os.ErrInvalid
	}
	m.inbounds = append(m.inbounds[:index], m.inbounds[index+1:]...)
	inboundScope := m.inboundScopes[inbound]
	delete(m.inboundScopes, inbound)
	m.access.Unlock()
	if inboundScope != nil {
		monitor := taskmonitor.New(m.logger, C.StopTimeout)
		name := "inbound/" + inbound.Type() + "[" + inbound.Tag() + "]"
		done := adapter.LogElapsed(m.logger, "close ", name)
		monitor.Start("close ", name)
		err := inboundScope.Close()
		monitor.Finish()
		done()
		if err != nil {
			return E.Cause(err, "close ", name)
		}
	}
	return nil
}

func closeInbound(inbound adapter.Inbound) error {
	if closer, ok := inbound.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func (m *Manager) Create(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any) error {
	inbound, err := m.registry.Create(ctx, router, logger, tag, outboundType, options)
	if err != nil {
		return err
	}
	m.access.Lock()
	if existsInbound, loaded := m.inboundByTag[tag]; loaded {
		if existsScope := m.inboundScopes[existsInbound]; existsScope != nil {
			if err = existsScope.Close(); err != nil {
				m.access.Unlock()
				return E.Cause(err, "close inbound/", existsInbound.Type(), "[", existsInbound.Tag(), "]")
			}
			delete(m.inboundScopes, existsInbound)
		} else if m.started {
			if err = closeInbound(existsInbound); err != nil {
				m.access.Unlock()
				return E.Cause(err, "close inbound/", existsInbound.Type(), "[", existsInbound.Tag(), "]")
			}
		}
		existsIndex := common.Index(m.inbounds, func(it adapter.Inbound) bool { return it == existsInbound })
		if existsIndex == -1 {
			m.access.Unlock()
			panic("invalid inbound index")
		}
		m.inbounds = append(m.inbounds[:existsIndex], m.inbounds[existsIndex+1:]...)
	}
	if m.started {
		name := "inbound/" + inbound.Type() + "[" + inbound.Tag() + "]"
		inboundScope := adapter.NewScope(m.scope.Context(), m.logger)
		m.inboundScopes[inbound] = inboundScope
		for _, stage := range adapter.ListStartStages {
			if stage > m.stage {
				break
			}
			done := adapter.LogElapsed(m.logger, stage, " ", name)
			startErr := inboundScope.Start(name, inbound, stage)
			done()
			if startErr != nil {
				delete(m.inboundScopes, inbound)
				_ = inboundScope.Close()
				m.access.Unlock()
				return E.Cause(startErr, stage, " ", name)
			}
		}
	}
	m.inbounds = append(m.inbounds, inbound)
	m.inboundByTag[tag] = inbound
	m.access.Unlock()
	return nil
}

func (m *Manager) close() error {
	m.access.Lock()
	scopes := m.inboundScopes
	m.inboundScopes = make(map[adapter.Inbound]*adapter.Scope)
	m.started = false
	m.scope = nil
	m.access.Unlock()
	var err error
	for inbound, scope := range scopes {
		name := "inbound/" + inbound.Type() + "[" + inbound.Tag() + "]"
		err = E.Append(err, scope.Close(), func(closeErr error) error {
			return E.Cause(closeErr, "close ", name)
		})
	}
	return err
}
