package extension

import "gopkg.in/yaml.v2"

type Config struct {
	// TODO: support for codedir not in root, multiple code dirs, location of schema files, etc.
	Name       string `yaml:"name"`
	Extensions []*struct {
		// TODO: placeholder, currently only support extensions in other local dirs, should be much more flexible
		Local string `yaml:"local,omitempty"`
	} `yaml:"extensions,omitempty"`
}

func ParseConfig(data []byte) (*Config, error) {
	cfg := Config{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
