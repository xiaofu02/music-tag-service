package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	mu sync.RWMutex

	AutoImport      AutoImportConfig `json:"auto_import"`
	DefaultSettings DefaultSettings  `json:"default_settings"`
	WatchFolders    []WatchFolder    `json:"watch_folders"`
}

type AutoImportConfig struct {
	Enabled      bool   `json:"enabled"`
	WatchPath    string `json:"watch_path"`
	Concurrency  int    `json:"concurrency"`
	AutoTag      bool   `json:"auto_tag"`
	Providers    []string `json:"providers"`
	Mode         string `json:"mode"`
	Overwrite    bool   `json:"overwrite"`
}

type DefaultSettings struct {
	Concurrency int      `json:"concurrency"`
	Providers   []string `json:"providers"`
	Mode        string   `json:"mode"`
	Overwrite   bool     `json:"overwrite"`
	SaveCover   bool     `json:"save_cover"`
	SaveLyrics  bool     `json:"save_lyrics"`
}

type WatchFolder struct {
	Path        string `json:"path"`
	Enabled     bool   `json:"enabled"`
	AutoTag    bool   `json:"auto_tag"`
	Concurrency int    `json:"concurrency"`
	Providers   []string `json:"providers"`
}

var defaultConfig = Config{
	AutoImport: AutoImportConfig{
		Enabled:      false,
		WatchPath:    "",
		Concurrency:  4,
		AutoTag:      true,
		Providers:    []string{"netease", "qmusic"},
		Mode:        "hard",
		Overwrite:   false,
	},
	DefaultSettings: DefaultSettings{
		Concurrency: 4,
		Providers:   []string{"netease", "qmusic"},
		Mode:        "hard",
		Overwrite:   false,
		SaveCover:   false,
		SaveLyrics:  false,
	},
	WatchFolders: []WatchFolder{},
}

var configDir string

func SetConfigDir(dir string) {
	configDir = dir
}

func GetConfigDir() string {
	return configDir
}

type Manager struct {
	configPath string
	cfg        Config
	mu         sync.RWMutex
}

func NewManager(exePath string) (*Manager, error) {
	cfgDir := filepath.Dir(exePath)
	configPath := filepath.Join(cfgDir, "config.json")

	m := &Manager{
		configPath: configPath,
		cfg:        defaultConfig,
	}

	if err := m.Load(); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if err := m.Save(); err != nil {
			return nil, err
		}
	}

	return m, nil
}

func (m *Manager) Load() error {
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := json.Unmarshal(data, &m.cfg); err != nil {
		m.cfg = defaultConfig
		return err
	}

	return nil
}

func (m *Manager) Save() error {
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(m.configPath, data, 0644)
}

func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *Manager) Update(newCfg Config) error {
	m.mu.Lock()
	m.cfg = newCfg
	m.mu.Unlock()

	return m.Save()
}

func (m *Manager) UpdateAutoImport(autoImport AutoImportConfig) error {
	m.mu.Lock()
	m.cfg.AutoImport = autoImport
	m.mu.Unlock()

	return m.Save()
}

func (m *Manager) UpdateDefaultSettings(settings DefaultSettings) error {
	m.mu.Lock()
	m.cfg.DefaultSettings = settings
	m.mu.Unlock()

	return m.Save()
}
