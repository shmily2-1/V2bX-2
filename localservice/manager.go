package localservice

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/shmily2-1/V2bX-2/conf"
	log "github.com/sirupsen/logrus"
)

var ErrRestartRequired = errors.New("service restart required")

type Status struct {
	AppliedRevision uint64
	ID              string
	Type            string
	State           string
	Error           string `json:"Error,omitempty"`
	Updated         time.Time
	Traffic         map[int]Traffic
	Peers           []PeerStatus   `json:"Peers,omitempty"`
	Process         *ProcessStatus `json:"Process,omitempty"`
}
type running struct {
	spec       conf.LocalService
	hash       string
	failedHash string
	instance   Instance
}
type Manager struct {
	mu      sync.Mutex
	config  *conf.Conf
	active  map[string]*running
	status  map[string]*Status
	stop    chan struct{}
	done    chan struct{}
	closed  bool
	started bool
	runLock *flock.Flock
}

func New(c *conf.Conf) *Manager {
	return &Manager{config: c, active: map[string]*running{}, status: map[string]*Status{}, stop: make(chan struct{}), done: make(chan struct{})}
}
func (m *Manager) StateDir() string { return filepath.Join(m.config.LocalServiceDir, ".state") }
func hashSpec(s conf.LocalService) string {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (m *Manager) Start() (result error) {
	if err := os.MkdirAll(m.StateDir(), 0700); err != nil {
		return err
	}
	m.runLock = flock.New(filepath.Join(m.StateDir(), "runtime.lock"))
	locked, err := m.runLock.TryLock()
	if err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("another process owns LocalServiceDir")
	}
	defer func() {
		if result != nil {
			_ = m.runLock.Close()
		}
	}()
	data, err := os.ReadFile(filepath.Join(m.StateDir(), "status.json"))
	if err == nil {
		if err = json.Unmarshal(data, &m.status); err != nil {
			return fmt.Errorf("invalid service status state")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if m.status == nil {
		m.status = map[string]*Status{}
	}
	specs, err := LoadSpecs(m.config)
	if err != nil {
		return err
	}
	m.mu.Lock()
	err = m.reconcile(specs)
	m.mu.Unlock()
	if err != nil {
		_ = m.closeInstances()
		return err
	}
	for _, s := range specs {
		if s.Enabled && m.active[s.ID] == nil {
			st := m.status[s.ID]
			_ = m.closeInstances()
			return fmt.Errorf("service %s failed to start: %s", s.ID, st.Error)
		}
	}
	m.started = true
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-ticker.C:
				if err := m.Reconcile(); err != nil {
					log.WithError(err).Error("Local service reconciliation failed; retained current instances")
				}
			}
		}
	}()
	return nil
}
func (m *Manager) collect(id string, r *running) error {
	if observer, ok := r.instance.(ProcessObserver); ok {
		state := observer.ProcessStatus()
		m.status[id].Process = &state
	}
	if observer, ok := r.instance.(PeerObserver); ok {
		peers, err := observer.PeerStatus()
		if err != nil {
			return err
		}
		m.status[id].Peers = peers
	}
	t, err := r.instance.Traffic()
	if err != nil {
		return err
	}
	s := m.status[id]
	for _, v := range t {
		old := s.Traffic[v.UID]
		old.UID = v.UID
		old.Upload += v.Upload
		old.Download += v.Download
		s.Traffic[v.UID] = old
	}
	return nil
}
func (m *Manager) Reconcile() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	specs, err := LoadSpecs(m.config)
	if err != nil {
		return err
	}
	return m.reconcile(specs)
}

// Startup applies and checks the same snapshot; concurrent CLI edits are
// picked up on the next reconciliation rather than inspected before applying.
func (m *Manager) reconcile(specs []conf.LocalService) error {
	var err error
	wanted := map[string]bool{}
	for _, raw := range specs {
		wanted[raw.ID] = true
		st := m.status[raw.ID]
		if st == nil {
			st = &Status{ID: raw.ID, Traffic: map[int]Traffic{}}
			m.status[raw.ID] = st
		}
		if st.Traffic == nil {
			st.Traffic = map[int]Traffic{}
		}
		st.Type, st.Updated = raw.Type, time.Now().UTC()
		r := m.active[raw.ID]
		var collectErr error
		if r != nil {
			collectErr = m.collect(raw.ID, r)
		}
		if !raw.Enabled {
			if r != nil {
				if err = m.stopInstance(raw.ID, r); err != nil {
					st.State, st.Error = "停止失败", err.Error()
					continue
				}
				delete(m.active, raw.ID)
			}
			st.State, st.Error = "已停用", ""
			st.AppliedRevision = raw.Revision
			continue
		}
		spec, alloc, err := Allocate(raw, filepath.Join(m.StateDir(), raw.ID+".addresses.json"))
		if err != nil {
			st.Error = err.Error()
			continue
		}
		hash := hashSpec(spec)
		if r != nil && r.failedHash == hash && r.instance.Health() == nil {
			st.State = "已保留旧配置"
			continue
		}
		if r != nil && r.hash == hash {
			if err = r.instance.Health(); err == nil {
				st.State, st.Error = "运行中", ""
				st.AppliedRevision = raw.Revision
				if collectErr != nil {
					st.State, st.Error = "计量异常", "traffic collection failed"
				}
				continue
			}
			st.State, st.Error = "异常", err.Error()
		}
		if r != nil && r.hash != hash {
			if updater, ok := r.instance.(LiveUpdater); ok {
				err = updater.Update(spec)
				if err == nil {
					r.spec, r.hash = spec, hash
					st.State, st.Error = "运行中", ""
					st.AppliedRevision = raw.Revision
					if alloc != nil {
						err = WriteJSON(filepath.Join(m.StateDir(), spec.ID+".addresses.json"), alloc)
					}
					if err != nil {
						st.Error = "allocation persistence failed"
					}
					continue
				}
				if !errors.Is(err, ErrRestartRequired) {
					st.State, st.Error = "已保留旧配置", err.Error()
					r.failedHash = hash
					continue
				}
			}
		}
		if r != nil {
			if err = m.stopInstance(spec.ID, r); err != nil {
				st.State, st.Error = "停止失败", err.Error()
				continue
			}
			delete(m.active, spec.ID)
		}
		inst, startErr := Open(spec, m.StateDir())
		if startErr == nil && alloc != nil {
			startErr = WriteJSON(filepath.Join(m.StateDir(), spec.ID+".addresses.json"), alloc)
			if startErr != nil {
				_ = inst.Close()
			}
		}
		if startErr != nil {
			st.State, st.Error = "启动失败", startErr.Error()
			if r != nil {
				restored, restoreErr := Open(r.spec, m.StateDir())
				if restoreErr == nil {
					r.instance = restored
					r.failedHash = hash
					m.active[spec.ID] = r
					st.State = "已恢复旧配置"
				} else {
					st.Error += "; previous service could not be restored"
				}
			}
			continue
		}
		m.active[spec.ID] = &running{spec: spec, hash: hash, instance: inst}
		st.State, st.Error = "运行中", ""
		st.AppliedRevision = raw.Revision
	}
	for id, r := range m.active {
		if !wanted[id] {
			if err = m.collect(id, r); err != nil {
				m.status[id].Error = "final traffic collection unavailable"
			}
			if err = m.stopInstance(id, r); err != nil {
				return err
			}
			delete(m.active, id)
			m.status[id].State = "已移除"
		}
	}
	return WriteJSON(filepath.Join(m.StateDir(), "status.json"), m.status)
}
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	close(m.stop)
	m.mu.Unlock()
	if m.started {
		<-m.done
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeInstances()
}
func (m *Manager) closeInstances() error {
	var errs []error
	for id, r := range m.active {
		errs = append(errs, m.collect(id, r), m.stopInstance(id, r))
		m.status[id].State = "已停止"
	}
	m.active = map[string]*running{}
	errs = append(errs, WriteJSON(filepath.Join(m.StateDir(), "status.json"), m.status))
	if m.runLock != nil {
		errs = append(errs, m.runLock.Close())
	}
	return errors.Join(errs...)
}

func (m *Manager) stopInstance(id string, r *running) error {
	err := r.instance.Close()
	if observer, ok := r.instance.(ProcessObserver); ok {
		state := observer.ProcessStatus()
		m.status[id].Process = &state
	}
	// In-process counters remain readable after all listeners/flows close.
	// External processes must be sampled before stopping their metric endpoint.
	if _, external := r.instance.(ProcessObserver); !external && err == nil {
		rows, collectErr := r.instance.Traffic()
		err = errors.Join(err, collectErr)
		for _, v := range rows {
			old := m.status[id].Traffic[v.UID]
			old.UID = v.UID
			old.Upload += v.Upload
			old.Download += v.Download
			m.status[id].Traffic[v.UID] = old
		}
	}
	m.status[id].Updated = time.Now().UTC()
	return err
}
