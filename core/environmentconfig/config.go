package environmentconfig

type SDK string

const (
	SDKGo     SDK = "go"
	SDKPython SDK = "python"
)

type Config struct {
	Root string `json:"root"`
	Name string `json:"name"`
	SDK  SDK    `json:"sdk,omitempty"`
}
