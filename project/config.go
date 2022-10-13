package project

type Config struct {
	Name       string               `json:"name"`
	Extensions map[string]Extension `json:"extensions,omitempty"`
	SDK        string               `json:"sdk,omitempty"`
}

type Extension struct {
	Local *LocalExtension `json:"local,omitempty"`
	Git   *GitExtension   `json:"git,omitempty"`
}

type LocalExtension struct {
	Path string `json:"path,omitempty"`
}

type GitExtension struct {
	Remote string `json:"remote,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Path   string `json:"path,omitempty"`
}
