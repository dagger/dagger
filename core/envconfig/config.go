package envconfig

import "path"

type SDK string

const (
	SDKGo     SDK = "go"
	SDKPython SDK = "python"
)

type Config struct {
	Root         string   `json:"root"`
	Name         string   `json:"name"`
	SDK          SDK      `json:"sdk,omitempty"`
	Include      []string `json:"include,omitempty"`
	Exclude      []string `json:"exclude,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

func NormalizeConfigPath(configPath string) string {
	// figure out if we were passed a path to a dagger.json file
	// or a parent dir that may contain such a file
	baseName := path.Base(configPath)
	if baseName == "dagger.json" {
		return configPath
	}
	return path.Join(configPath, "dagger.json")
}
