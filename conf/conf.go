package conf

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/shmily2-1/V2bX-2/common/json5"

	"encoding/json/v2"
)

type Conf struct {
	LocalServices   []LocalService `json:"LocalServices,omitempty"`
	LocalServiceDir string         `json:"LocalServiceDir,omitempty"`
	LogConfig       LogConfig      `json:"Log"`
	CoresConfig     []CoreConfig   `json:"Cores"`
	NodeConfig      []NodeConfig   `json:"Nodes"`
}

func New() *Conf {
	return &Conf{
		LogConfig: LogConfig{
			Level:  "info",
			Output: "",
		},
	}
}

func (p *Conf) LoadFromPath(filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open config file error: %s", err)
	}
	defer f.Close()

	reader := json5.NewTrimNodeReader(f)
	data, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("read config file error: %s", err)
	}

	candidate := New()
	err = json.Unmarshal(data, candidate)
	if err != nil {
		return fmt.Errorf("unmarshal config error: %s", err)
	}

	base, err := filepath.Abs(filepath.Dir(filePath))
	if err != nil {
		return err
	}
	if candidate.LocalServiceDir == "" {
		candidate.LocalServiceDir = filepath.Join(base, "services")
	}
	if !filepath.IsAbs(candidate.LocalServiceDir) {
		candidate.LocalServiceDir = filepath.Join(base, candidate.LocalServiceDir)
	}
	for i := range candidate.LocalServices {
		candidate.LocalServices[i].ResolvePaths(base)
	}
	for i := range candidate.NodeConfig {
		if candidate.NodeConfig[i].Options.TrafficStateDir == "" {
			candidate.NodeConfig[i].Options.TrafficStateDir = filepath.Join(base, "traffic")
		}
		options := &candidate.NodeConfig[i].Options
		if !filepath.IsAbs(options.TrafficStateDir) {
			options.TrafficStateDir = filepath.Join(base, options.TrafficStateDir)
		}
		if cert := options.CertConfig; cert != nil && cert.ECHKeyFile != "" && !filepath.IsAbs(cert.ECHKeyFile) {
			cert.ECHKeyFile = filepath.Join(base, cert.ECHKeyFile)
		}
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*p = *candidate
	return nil
}
