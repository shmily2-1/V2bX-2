package hy2

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/apernet/hysteria/core/v2/server"
	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/common/porthop"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

type Hysteria2node struct {
	redirect      io.Closer
	closed        bool
	Hy2server     server.Server
	Tag           string
	Logger        *zap.Logger
	EventLogger   server.EventLogger
	TrafficLogger server.TrafficLogger
}

func (h *Hysteria2) AddNode(tag string, info *panel.NodeInfo, config *conf.Options) error {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	if h.closed {
		return fmt.Errorf("hysteria2 core is closed")
	}
	if _, exists := h.Hy2nodes[tag]; exists {
		return fmt.Errorf("node %q already exists", tag)
	}
	if info.Type != "hysteria2" || info.Hysteria2 == nil {
		return fmt.Errorf("hysteria2 core requires a Hysteria2 node")
	}
	var err error
	hyconfig := &server.Config{}
	var c serverConfig
	v := viper.New()
	if len(config.Hysteria2ConfigPath) != 0 {
		v.SetConfigFile(config.Hysteria2ConfigPath)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("read hysteria2 config: %w", err)
		}
		if err := v.Unmarshal(&c); err != nil {
			return fmt.Errorf("parse hysteria2 config: %w", err)
		}
	}
	n := Hysteria2node{
		Tag:    tag,
		Logger: h.Logger,
		EventLogger: &serverLogger{
			Tag:    tag,
			logger: h.Logger,
		},
		TrafficLogger: &HookServer{
			Tag:                   tag,
			logger:                h.Logger,
			ReportMinTrafficBytes: config.ReportMinTraffic * 1024,
		},
	}

	hyconfig, err = n.getHyConfig(info, config, &c)
	if err != nil {
		return err
	}
	hyconfig.Authenticator = h.Auth
	s, err := server.NewServer(hyconfig)
	if err != nil {
		_ = hyconfig.Conn.Close()
		return err
	}
	// Bind/validate the actual core first. Do not redirect traffic into a
	// listener whose certificate/configuration failed to initialize.
	redirect, err := porthop.Setup(tag, config.ListenIP, info.Common.ServerPort, info.Hysteria2.Ports)
	hyconfig.Cleanup = redirect
	n.redirect = redirect
	n.Hy2server = s
	if err != nil {
		closeErr := s.Close()
		if closeErr != nil && redirect != nil {
			n.closed = true
			h.Hy2nodes[tag] = n // Keep failed cleanup reachable by Close/DelNode.
		}
		return errors.Join(err, closeErr)
	}
	h.Hy2nodes[tag] = n
	go func() {
		if err := s.Serve(); err != nil {
			if !strings.Contains(err.Error(), "quic: server closed") {
				h.Logger.Error("Server Error", zap.Error(err))
			}
		}
	}()
	return nil
}

func (h *Hysteria2) DelNode(tag string) error {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	return h.closeNode(tag)
}

// Caller holds nodesMu. Do not call server.Close twice, but allow retrying
// firewall cleanup after a transient external command failure.
func (h *Hysteria2) closeNode(tag string) error {
	n, ok := h.Hy2nodes[tag]
	if !ok {
		return fmt.Errorf("node %q not found", tag)
	}
	var err error
	if !n.closed {
		err = n.Hy2server.Close() // Also closes server.Config.Cleanup.
		n.closed = true
	} else if n.redirect != nil {
		err = n.redirect.Close()
	}
	if err != nil {
		h.Hy2nodes[tag] = n
		return err
	}
	delete(h.Hy2nodes, tag)
	return err
}
