package localservice

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/shmily2-1/V2bX-2/conf"
)

func WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return WritePrivate(path, append(data, '\n'))
}
func WritePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
func LoadSpecs(c *conf.Conf) ([]conf.LocalService, error) {
	result := append([]conf.LocalService{}, c.LocalServices...)
	paths, err := filepath.Glob(filepath.Join(c.LocalServiceDir, "*.json"))
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var s conf.LocalService
		if err = json.Unmarshal(data, &s); err != nil {
			return nil, fmt.Errorf("invalid service file %s", filepath.Base(p))
		}
		if filepath.Base(p) != s.ID+".json" {
			return nil, fmt.Errorf("service filename/ID mismatch")
		}
		s.ResolvePaths(c.LocalServiceDir)
		result = append(result, s)
	}
	ids := map[string]bool{}
	for _, s := range result {
		if ids[s.ID] {
			return nil, fmt.Errorf("duplicate service ID %s", s.ID)
		}
		ids[s.ID] = true
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("service %s: %w", s.ID, err)
		}
	}
	// Separate service networks must not overlap; the authenticated source
	// address then has an unambiguous node and peer owner.
	if err := validatePools(result); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}
func Edit(c *conf.Conf, id string, update func(*conf.LocalService) error) error {
	if !conf.ValidServiceID(id) {
		return fmt.Errorf("invalid service ID")
	}
	if err := os.MkdirAll(c.LocalServiceDir, 0700); err != nil {
		return err
	}
	lock := flock.New(filepath.Join(c.LocalServiceDir, ".edit.lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("service configuration is being edited")
	}
	defer lock.Close()
	path := filepath.Join(c.LocalServiceDir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	s := conf.LocalService{ID: id}
	if len(data) > 0 {
		if err = json.Unmarshal(data, &s); err != nil {
			return err
		}
	}
	if err = update(&s); err != nil {
		return err
	}
	if s.ID != id {
		return fmt.Errorf("service ID cannot be changed")
	}
	if s.Type != "" {
		resolved := s
		resolved.ResolvePaths(c.LocalServiceDir)
		if err = resolved.Validate(); err != nil {
			return err
		}
	}
	if len(data) > 0 {
		backup := path + ".bak-" + time.Now().UTC().Format("20060102T150405.000000000")
		if err = WritePrivate(backup, data); err != nil {
			return err
		}
	}
	if s.Type == "" {
		return os.Remove(path)
	}
	return WriteJSON(path, s)
}

func Redacted(s conf.LocalService) conf.LocalService {
	if s.WireGuard != nil {
		w := *s.WireGuard
		w.Peers = append([]conf.WireGuardPeer{}, w.Peers...)
		for i := range w.Peers {
			if w.Peers[i].PreSharedKey != "" {
				w.Peers[i].PreSharedKey = "[redacted]"
			}
		}
		s.WireGuard = &w
	}
	return s
}
func ReadSecret(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
