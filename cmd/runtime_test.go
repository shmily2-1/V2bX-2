package cmd

import (
	"errors"
	"testing"

	"github.com/shmily2-1/V2bX-2/conf"
	vCore "github.com/shmily2-1/V2bX-2/core"
	"github.com/shmily2-1/V2bX-2/node"
)

type runtimeCore struct {
	vCore.Core
	running bool
	fail    bool
}

func (r *runtimeCore) Start() error {
	if r.fail {
		return errors.New("intentional start failure")
	}
	r.running = true
	return nil
}
func (r *runtimeCore) Close() error { r.running = false; return nil }
func (r *runtimeCore) Type() string { return "xray" }

func TestRuntimeRestoresPreviousConfigurationAfterFailedStart(t *testing.T) {
	previous := &conf.Conf{CoresConfig: []conf.CoreConfig{{Type: "xray"}}}
	prepare := func(c *conf.Conf) (*serverRuntime, error) {
		return &serverRuntime{config: c, core: &runtimeCore{fail: c.CoresConfig[0].Name == "fail"}, nodes: node.New()}, nil
	}
	current, _ := prepare(previous)
	if err := current.start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = current.close() }()
	if err := replaceRuntimeWith(&current, &conf.Conf{CoresConfig: []conf.CoreConfig{{Type: "xray", Name: "fail"}}}, prepare); err == nil {
		t.Fatal("reported failed candidate as successful")
	}
	if current.config != previous || !current.core.(*runtimeCore).running {
		t.Fatal("previous runtime was not restored")
	}
}

func TestRuntimePreparationFailureKeepsCurrentRunning(t *testing.T) {
	current := &serverRuntime{config: &conf.Conf{}, core: &runtimeCore{running: true}, nodes: node.New()}
	if err := replaceRuntimeWith(&current, &conf.Conf{}, func(*conf.Conf) (*serverRuntime, error) { return nil, errors.New("invalid candidate") }); err == nil {
		t.Fatal("accepted candidate")
	}
	if !current.core.(*runtimeCore).running {
		t.Fatal("closed active runtime before validating candidate")
	}
}
