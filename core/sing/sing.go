package sing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
	"github.com/shmily2-1/V2bX-2/conf"
	vCore "github.com/shmily2-1/V2bX-2/core"
)

var _ vCore.Core = (*Sing)(nil)

type DNSConfig struct {
	Servers []map[string]interface{} `json:"servers"`
	Rules   []map[string]interface{} `json:"rules"`
}

type Sing struct {
	nodesMu                   sync.RWMutex
	portRedirects             map[string]io.Closer
	closed                    bool
	box                       *box.Box
	ctx                       context.Context
	hookServer                *HookServer
	router                    adapter.Router
	logFactory                log.Factory
	users                     *UserMap
	nodeReportMinTrafficBytes map[string]int64
}

type UserMap struct {
	uidMap  map[string]int
	mapLock sync.RWMutex
}

func init() {
	vCore.RegisterCore("sing", New)
}

func New(c *conf.CoreConfig) (vCore.Core, error) {
	ctx := context.Background()
	ctx = box.Context(ctx, include.InboundRegistry(), include.OutboundRegistry(), include.EndpointRegistry(), include.DNSTransportRegistry(), include.ServiceRegistry(), include.CertificateProviderRegistry())
	options := option.Options{}
	if len(c.SingConfig.OriginalPath) != 0 {
		data, err := os.ReadFile(c.SingConfig.OriginalPath)
		if err != nil {
			return nil, fmt.Errorf("read original config error: %s", err)
		}
		options, err = json.UnmarshalExtendedContext[option.Options](ctx, data)
		if err != nil {
			return nil, fmt.Errorf("unmarshal original config error: %s", err)
		}
	}
	options.Log = &option.LogOptions{
		Disabled:  c.SingConfig.LogConfig.Disabled,
		Level:     c.SingConfig.LogConfig.Level,
		Timestamp: c.SingConfig.LogConfig.Timestamp,
		Output:    c.SingConfig.LogConfig.Output,
	}
	options.NTP = &option.NTPOptions{
		Enabled:       c.SingConfig.NtpConfig.Enable,
		WriteToSystem: true,
		ServerOptions: option.ServerOptions{
			Server:     c.SingConfig.NtpConfig.Server,
			ServerPort: c.SingConfig.NtpConfig.ServerPort,
		},
	}
	os.Setenv("SING_DNS_PATH", "")
	b, err := box.New(box.Options{
		Context: ctx,
		Options: options,
	})
	if err != nil {
		return nil, err
	}
	hs := &HookServer{
		counter: sync.Map{},
	}
	b.Router().AppendTracker(hs)
	return &Sing{
		ctx:        ctx,
		box:        b,
		hookServer: hs,
		router:     b.Router(),
		logFactory: b.LogFactory(),
		users: &UserMap{
			uidMap: make(map[string]int),
		},
		nodeReportMinTrafficBytes: make(map[string]int64),
		portRedirects:             make(map[string]io.Closer),
	}, nil
}

func (b *Sing) Start() error {
	return b.box.Start()
}

func (b *Sing) Close() error {
	b.nodesMu.Lock()
	defer b.nodesMu.Unlock()
	var errs []error
	if !b.closed {
		errs = append(errs, b.box.Close())
		b.closed = true
	}
	for tag, redirect := range b.portRedirects {
		if err := redirect.Close(); err != nil {
			errs = append(errs, err)
		} else {
			delete(b.portRedirects, tag)
		}
	}
	return errors.Join(errs...)
}

func (b *Sing) Protocols() []string {
	return []string{
		"vmess",
		"vless",
		"shadowsocks",
		"trojan",
		"tuic",
		"anytls",
		"hysteria",
		"hysteria2",
	}
}

func (b *Sing) Type() string {
	return "sing"
}
