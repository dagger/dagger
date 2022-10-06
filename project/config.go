package project

type Config struct {
	Name       string       `json:"name"`
	Extensions []*Extension `json:"extensions,omitempty"`
	SDK        string       `json:"sdk,omitempty"`
}

type Extension struct {
	Local string     `json:"local,omitempty"`
	Git   *GitSource `json:"git,omitempty"`
}

type GitSource struct {
	Remote string `json:"remote,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Path   string `json:"path,omitempty"`
}
