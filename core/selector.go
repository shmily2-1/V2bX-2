package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
)

type Selector struct {
	cores map[string]Core
	order []string
	nodes sync.Map
}

func NewSelector(c []conf.CoreConfig) (Core, error) {
	cs := make(map[string]Core, len(c))
	order := make([]string, 0, len(c))
	for _, t := range c {
		f, ok := cores[strings.ToLower(t.Type)]
		if !ok {
			return nil, errors.New("unknown core type: " + t.Type)
		}
		core1, err := f(&t)
		if err != nil {
			return nil, err
		}
		name := t.Name
		if name == "" {
			name = t.Type
		}
		if _, exists := cs[name]; exists {
			return nil, fmt.Errorf("duplicate core name %q", name)
		}
		cs[name] = core1
		order = append(order, name)
	}
	return &Selector{
		cores: cs,
		order: order,
	}, nil
}

func (s *Selector) Start() error {
	for i := range s.cores {
		err := s.cores[i].Start()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Selector) Close() error {
	var errs []error
	for i := range s.cores {
		if err := s.cores[i].Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func isSupported(protocol string, protocols []string) bool {
	for i := range protocols {
		if protocol == protocols[i] {
			return true
		}
	}
	return false
}

func (s *Selector) AddNode(tag string, info *panel.NodeInfo, option *conf.Options) error {
	var core Core
	if len(option.CoreName) > 0 {
		// use name to select core
		if c, ok := s.cores[option.CoreName]; ok {
			core = c
		}
	} else {
		// use type to select core
		order := s.order
		if len(order) == 0 {
			for name := range s.cores {
				order = append(order, name)
			}
			sort.Strings(order)
		}
		for _, name := range order {
			c := s.cores[name]
			if len(option.Core) == 0 {
				if !isSupported(info.Type, c.Protocols()) {
					continue
				}
			} else if option.Core != c.Type() {
				continue
			}
			if err := ValidateNode(c.Type(), info, option); err != nil {
				continue
			}
			core = c
			break
		}
	}
	if core == nil {
		return fmt.Errorf("no configured core supports node %s and its transport; set Core/CoreName to a supported combination", info.Type)
	}
	if option.Core != "" && option.Core != core.Type() {
		return fmt.Errorf("Core and CoreName select different core types")
	}
	if err := ValidateNode(core.Type(), info, option); err != nil {
		return err
	}
	if len(option.Core) == 0 {
		// Generic paths were already resolved relative to the config file.
		// Decode only core-specific options so those paths are not overwritten.
		var specific any
		switch core.Type() {
		case "xray":
			option.XrayOptions = conf.NewXrayOptions()
			specific = option.XrayOptions
		case "sing":
			option.SingOptions = conf.NewSingOptions()
			specific = option.SingOptions
		}
		if specific != nil && len(option.RawOptions) > 0 {
			if err := json.Unmarshal(option.RawOptions, specific); err != nil {
				return fmt.Errorf("unmarshal core options: %w", err)
			}
		}
		option.Core = core.Type()
		option.RawOptions = nil
	}
	err := core.AddNode(tag, info, option)
	if err != nil {
		return err
	}
	s.nodes.Store(tag, core)
	return nil
}

func (s *Selector) DelNode(tag string) error {
	if t, e := s.nodes.Load(tag); e {
		err := t.(Core).DelNode(tag)
		if err != nil {
			return err
		}
		s.nodes.Delete(tag)
		return nil
	}
	return errors.New("the node is not have")
}

func (s *Selector) AddUsers(p *AddUsersParams) (added int, err error) {
	t, e := s.nodes.Load(p.Tag)
	if !e {
		return 0, errors.New("the node is not have")
	}
	return t.(Core).AddUsers(p)
}

func (s *Selector) GetUserTrafficSlice(tag string, reset bool) ([]panel.UserTraffic, error) {
	t, e := s.nodes.Load(tag)
	if !e {
		return nil, errors.New("the node is not have")
	}
	return t.(Core).GetUserTrafficSlice(tag, reset)
}

func (s *Selector) DelUsers(users []panel.UserInfo, tag string, info *panel.NodeInfo) error {
	t, e := s.nodes.Load(tag)
	if !e {
		return errors.New("the node is not have")
	}
	return t.(Core).DelUsers(users, tag, info)
}

func (s *Selector) Protocols() []string {
	protocols := make([]string, 0)
	for i := range s.cores {
		protocols = append(protocols, s.cores[i].Protocols()...)
	}
	return protocols
}

func (s *Selector) Type() string {
	t := "Selector("
	var flag bool
	for n, c := range s.cores {
		if flag {
			t += " "
		} else {
			flag = true
		}
		if len(n) == 0 {
			t += c.Type()
		} else {
			t += n
		}
	}
	t += ")"
	return t
}
