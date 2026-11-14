package localservice

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/shmily2-1/V2bX-2/conf"
)

type childProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	mu   sync.Mutex
	exit error
}

func verifyBackend(b *conf.BackendService) error {
	f, err := os.Open(b.Executable)
	if err != nil {
		return fmt.Errorf("backend executable is unavailable")
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	expected, err := hex.DecodeString(b.SHA256)
	if err != nil || !equalBytes(expected, h.Sum(nil)) {
		return fmt.Errorf("backend executable SHA256 mismatch")
	}
	return nil
}
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var x byte
	for i := range a {
		x |= a[i] ^ b[i]
	}
	return x == 0
}
func startChild(b *conf.BackendService, args ...string) (*childProcess, error) {
	if err := verifyBackend(b); err != nil {
		return nil, err
	}
	c := &childProcess{cmd: exec.Command(b.Executable, args...), done: make(chan struct{})}
	// Backends may print their secret/configuration on failures. Keep raw
	// output private and report exit status through the supervisor instead.
	c.cmd.Stdout, c.cmd.Stderr = io.Discard, io.Discard
	if err := c.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start backend failed: %w", err)
	}
	go func() { err := c.cmd.Wait(); c.mu.Lock(); c.exit = err; c.mu.Unlock(); close(c.done) }()
	return c, nil
}
func (c *childProcess) Health() error {
	select {
	case <-c.done:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.exit == nil {
			return fmt.Errorf("backend exited")
		}
		return fmt.Errorf("backend exited: %w", c.exit)
	default:
		return nil
	}
}
func (c *childProcess) Status() ProcessStatus {
	state := ProcessStatus{PID: c.cmd.Process.Pid}
	select {
	case <-c.done:
		c.mu.Lock()
		code := c.cmd.ProcessState.ExitCode()
		c.mu.Unlock()
		state.ExitCode = &code
	default:
	}
	return state
}
func (c *childProcess) Close() error {
	select {
	case <-c.done:
		return nil
	default:
	}
	_ = c.cmd.Process.Signal(os.Interrupt)
	select {
	case <-c.done:
		return nil
	case <-time.After(2 * time.Second):
	}
	if err := c.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-c.done:
		return nil
	case <-time.After(3 * time.Second):
		return fmt.Errorf("backend did not stop")
	}
}
