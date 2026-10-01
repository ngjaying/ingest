package device

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed default.yaml
var defaultYAML []byte

type fileFormat struct {
	Version string        `yaml:"version"`
	Devices []deviceEntry `yaml:"devices"`
}

type deviceEntry struct {
	ID           string        `yaml:"id"`
	Name         string        `yaml:"name"`
	Manufacturer string        `yaml:"manufacturer"`
	Detect       detectSection `yaml:"detect"`
	Target       string        `yaml:"target,omitempty"`
}

type detectSection struct {
	VolumeLabels []string `yaml:"volume_labels"`
	Directories  []string `yaml:"directories"`
	FilePatterns []string `yaml:"file_patterns"`
}

// DefaultConfigPath 返回设备规则配置文件的默认路径。
// 遵循 XDG Base Directory：$XDG_CONFIG_HOME/ingest/devices.yaml，
// 未设时回落到 ~/.config/ingest/devices.yaml。
func DefaultConfigPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ingest", "devices.yaml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ingest", "devices.yaml")
}

// LoadOrInit 从指定路径加载规则；文件不存在则把内嵌的出厂默认写出去再加载。
// 这样工具开箱即用，但所有规则都"住在"用户可见、可编辑的文件里——
// 而不是隐藏在二进制里写死。
func LoadOrInit(path string) ([]Rule, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create config dir: %w", err)
		}
		if err := os.WriteFile(path, defaultYAML, 0o644); err != nil {
			return nil, fmt.Errorf("write default config: %w", err)
		}
	} else if err != nil {
		return nil, err
	}
	return LoadFromFile(path)
}

func LoadFromFile(path string) ([]Rule, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f fileFormat
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	rules := make([]Rule, 0, len(f.Devices))
	for i, d := range f.Devices {
		if d.ID == "" {
			return nil, fmt.Errorf("%s: device entry #%d missing id", path, i+1)
		}
		rules = append(rules, Rule{
			ID:           d.ID,
			Name:         d.Name,
			Manufacturer: d.Manufacturer,
			VolumeLabels: d.Detect.VolumeLabels,
			Directories:  d.Detect.Directories,
			FilePatterns: d.Detect.FilePatterns,
			Target:       d.Target,
		})
	}
	return rules, nil
}
