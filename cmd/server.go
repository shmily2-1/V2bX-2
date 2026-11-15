package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"

	"github.com/shmily2-1/V2bX-2/conf"
	vCore "github.com/shmily2-1/V2bX-2/core"
	"github.com/shmily2-1/V2bX-2/limiter"
	"github.com/shmily2-1/V2bX-2/node"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	config string
	watch  bool
)

var serverCommand = cobra.Command{
	Use: "server", Short: "Run V2bX server",
	RunE: serverHandle, Args: cobra.NoArgs,
}

func init() {
	serverCommand.PersistentFlags().StringVarP(&config, "config", "c", "/etc/V2bX/config.json", "config file path")
	serverCommand.PersistentFlags().BoolVarP(&watch, "watch", "w", true, "watch file path change")
	command.AddCommand(&serverCommand)
}

type serverRuntime struct {
	config *conf.Conf
	core   vCore.Core
	nodes  *node.Node
}

func prepareRuntime(c *conf.Conf) (state *serverRuntime, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("core construction failed: %v", recovered)
			state = nil
		}
	}()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var engine vCore.Core
	if len(c.CoresConfig) > 0 {
		engine, err = vCore.NewCore(c.CoresConfig)
		if err != nil {
			return nil, err
		}
	}
	state = &serverRuntime{config: c, core: engine, nodes: node.New()}
	return state, nil
}

func (s *serverRuntime) start() error {
	if s.core != nil {
		if err := s.core.Start(); err != nil {
			_ = s.core.Close()
			return err
		}
	}
	if err := s.nodes.Start(s.config.NodeConfig, s.core); err != nil {
		if s.core != nil {
			_ = s.core.Close()
		}
		return err
	}
	return nil
}

func (s *serverRuntime) close() error {
	var coreErr error
	nodeErr := s.nodes.Close()
	if s.core != nil {
		coreErr = s.core.Close()
	}
	return errors.Join(nodeErr, coreErr)
}

func replaceRuntime(current **serverRuntime, candidate *conf.Conf) error {
	return replaceRuntimeWith(current, candidate, prepareRuntime)
}

func replaceRuntimeWith(current **serverRuntime, candidate *conf.Conf, prepare func(*conf.Conf) (*serverRuntime, error)) error {
	next, err := prepare(candidate)
	if err != nil {
		return err
	}
	previous := (*current).config
	closeErr := (*current).close()
	if closeErr == nil {
		if err = next.start(); err == nil {
			*current = next
			return nil
		}
	} else {
		err = closeErr
		if next.core != nil {
			_ = next.core.Close()
		}
	}
	restored, restoreErr := prepare(previous)
	if restoreErr == nil {
		restoreErr = restored.start()
	}
	if restoreErr != nil {
		return errors.Join(err, fmt.Errorf("restore previous runtime: %w", restoreErr))
	}
	*current = restored
	return fmt.Errorf("candidate rejected, previous runtime restored: %w", err)
}

func serverHandle(_ *cobra.Command, _ []string) (result error) {
	showVersion()
	c := conf.New()
	if err := c.LoadFromPath(config); err != nil {
		return err
	}
	if level, err := log.ParseLevel(c.LogConfig.Level); err == nil {
		log.SetLevel(level)
	}
	if c.LogConfig.Output != "" {
		f, err := os.OpenFile(c.LogConfig.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.WithError(err).Error("Open log file failed, keeping current log output")
		} else {
			previous := log.StandardLogger().Out
			log.SetOutput(f)
			defer func() { log.SetOutput(previous); f.Close() }()
		}
	}
	limiter.Init()
	current, err := prepareRuntime(c)
	if err != nil {
		return err
	}
	if err := current.start(); err != nil {
		return err
	}
	log.Info("Nodes started")
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	var access sync.Mutex
	defer func() {
		cancel()
		access.Lock()
		defer access.Unlock()
		if err := current.close(); err != nil {
			log.WithError(err).Error("Close runtime failed")
			result = errors.Join(result, err)
		}
	}()
	if watch {
		err = c.WatchContext(ctx, config, os.Getenv("XRAY_DNS_PATH"), os.Getenv("SING_DNS_PATH"), func(candidate *conf.Conf) error {
			access.Lock()
			defer access.Unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := replaceRuntime(&current, candidate); err != nil {
				return err
			}
			runtime.GC()
			return nil
		})
		if err != nil {
			return err
		}
	}
	runtime.GC()
	<-ctx.Done()
	return nil
}
