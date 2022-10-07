package project

type projectConfig struct {
	Name         string        `yaml:"name"`
	Dependencies []*Dependency `yaml:"dependencies,omitempty"`
	Scripts      []*Script     `yaml:"scripts,omitempty"`
	Extensions   []*Extension  `yaml:"extensions,omitempty"`
}

type Script struct {
	Path string `yaml:"path" json:"path"`
	SDK  string `yaml:"sdk" json:"sdk"`
}

type Extension struct {
	Path   string `yaml:"path" json:"path"`
	SDK    string `yaml:"sdk" json:"sdk"`
	Schema string `yaml:"schema" json:"schema"`
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
