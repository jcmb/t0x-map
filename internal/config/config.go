package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen      string   `yaml:"listen"`
	BasePath    string   `yaml:"base_path"`
	DataRoot    string   `yaml:"data_root"`
	DBPath      string   `yaml:"db_path"`
	ViewdatPath string   `yaml:"viewdat_path"`
	T0x2t0xPath string   `yaml:"t0x2t0x_path"`
	PythonPath  string   `yaml:"python_path"`
	Groups      []string `yaml:"groups"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8787"
	}
	if c.ViewdatPath == "" {
		c.ViewdatPath = "viewdat"
	}
	if c.T0x2t0xPath == "" {
		c.T0x2t0xPath = "/usr/local/bin/t0x2t0x"
	}
	if c.PythonPath == "" {
		c.PythonPath = "python3"
	}
	c.BasePath = strings.TrimSuffix(c.BasePath, "/")
}

func (c *Config) validate() error {
	if c.DataRoot == "" {
		return fmt.Errorf("data_root is required")
	}
	if c.DBPath == "" {
		return fmt.Errorf("db_path is required")
	}
	if len(c.Groups) == 0 {
		return fmt.Errorf("groups must list at least one group")
	}
	return nil
}
