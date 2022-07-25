package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

	"gopkg.in/yaml.v2"
)

type Config struct {
	Path    string             `yaml:"-,omitempty"`
	Actions map[string]*Action `yaml:"actions,omitempty"`
}

type Action struct {
	Local      string `yaml:"local,omitempty"`
	Image      string `yaml:"image,omitempty"`
	Dockerfile string `yaml:"dockerfile,omitempty"`
}

func ParseFile(f string) (*Config, error) {
	data, err := os.ReadFile(f)
	if err != nil {
		return nil, err
	}

	cfg := Config{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	for _, action := range cfg.Actions {
		if action.Local != "" {
			action.Local = path.Join(filepath.Dir(f), action.Local)
		}
	}

	loaded, err := yaml.Marshal(&cfg)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "Loading:\n%s\n", string(loaded))

	return &cfg, nil
}
