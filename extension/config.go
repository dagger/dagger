package extension

import "gopkg.in/yaml.v2"

type Config struct {
	Name         string        `yaml:"name"`
	Dependencies []*Dependency `yaml:"dependencies,omitempty"`
	Sources      []*Source     `yaml:"sources,omitempty"`
}

type Source struct {
	Path string `yaml:"path"`
	SDK  string `yaml:"sdk"`
}

type Dependency struct {
	Local string     `yaml:"local,omitempty"`
	Git   *GitSource `yaml:"git,omitempty"`
}

type GitSource struct {
	Remote string `yaml:"remote,omitempty"`
	Ref    string `yaml:"ref,omitempty"`
	Path   string `yaml:"path,omitempty"`
}

func ParseConfig(data []byte) (*Config, error) {
	cfg := Config{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
