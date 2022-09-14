package project

import (
	"gopkg.in/yaml.v2"
)

type Config struct {
	Name         string        `yaml:"name",json:"name"`
	Dependencies []*Dependency `yaml:"dependencies,omitempty",json:"dependencies"`
	Scripts      []*Script     `yaml:"scripts,omitempty",json:"scripts"`
	Extensions   []*Extension  `yaml:"extensions,omitempty",json:"extensions"`
}

type Script struct {
	Path string `yaml:"path",json:"path"`
	SDK  string `yaml:"sdk",json:"sdk"`
}

type Extension struct {
	Path string `yaml:"path",json:"path"`
	SDK  string `yaml:"sdk",json:"sdk"`

	// internal-only fields for tracking state
	Schema string `yaml:"-"`
}

type Dependency struct {
	Local string     `yaml:"local,omitempty",json:"local"`
	Git   *GitSource `yaml:"git,omitempty",json:"git"`
}

type GitSource struct {
	Remote string `yaml:"remote,omitempty",json:"remote"`
	Ref    string `yaml:"ref,omitempty",json:"ref"`
	Path   string `yaml:"path,omitempty",json:"path"`
}

func ParseConfig(data []byte) (*Config, error) {
	cfg := Config{}
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
