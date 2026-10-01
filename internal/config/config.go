// Package config 加载 ~/.config/ingest/config.yaml 中的全局设置。
//
// 与 internal/device 的 devices.yaml 平级：那个管"哪些设备长什么样"，
// 本包管"工具自身行为"。两者分离避免互相污染。
package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed default.yaml
var defaultYAML []byte

// Settings 是配置文件解码后的运行时表示。所有字段都有合理默认值，
// 文件缺失或字段缺失时回退到默认。
type Settings struct {
	// GapDays：自动分段时允许的日期间隔阈值。详细见 default.yaml 的注释。
	GapDays int `yaml:"gap_days"`
	// StagingDir：两段式中转目录，为空表示单段直拷。
	StagingDir string `yaml:"staging_dir"`
	// CleanupStaging：转拷成功后是否删除 staging 批次目录。
	CleanupStaging bool `yaml:"cleanup_staging"`
	// Hooks：批后外部命令。
	Hooks Hooks `yaml:"hooks"`
}

// Hooks 批后处理命令表。
type Hooks struct {
	AfterBatch []string `yaml:"after_batch"`
}

// Defaults 返回内置默认值。即便配置文件读不到，调用方也总能拿到可用 Settings。
func Defaults() Settings {
	return Settings{GapDays: 1, CleanupStaging: true}
}

// DefaultConfigPath 返回 XDG 规范下的配置文件位置。
func DefaultConfigPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ingest", "config.yaml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ingest", "config.yaml")
}

// LoadOrInit 读取 path 处的配置；文件不存在时把内嵌默认写到该路径再读。
// 解析失败返回错误；文件存在但缺字段时，缺的字段用 Defaults 填。
func LoadOrInit(path string) (Settings, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return Settings{}, fmt.Errorf("create config dir: %w", err)
		}
		if err := os.WriteFile(path, defaultYAML, 0o644); err != nil {
			return Settings{}, fmt.Errorf("write default config: %w", err)
		}
	} else if err != nil {
		return Settings{}, err
	}
	return loadFromFile(path)
}

func loadFromFile(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Settings{}, fmt.Errorf("read %s: %w", path, err)
	}
	s := Defaults()
	if err := yaml.Unmarshal(data, &s); err != nil {
		return Settings{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if s.GapDays < 0 {
		return Settings{}, fmt.Errorf("%s: gap_days must be >= 0, got %d", path, s.GapDays)
	}
	return s, nil
}
