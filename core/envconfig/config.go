package envconfig

type SDK string

const (
	SDKGo     SDK = "go"
	SDKPython SDK = "python"
)

type Config struct {
	Root    string   `json:"root"`
	Name    string   `json:"name"`
	SDK     SDK      `json:"sdk,omitempty"`
	Include []string `json:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
	// TODO: support non-local environments
	Dependencies []string `json:"dependencies,omitempty"`
}
