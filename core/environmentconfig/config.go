package environmentconfig

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
)

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

func ParseEnvURL(urlStr string) (*ParsedEnvURL, error) {
	url, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config path: %w", err)
	}
	switch url.Scheme {
	case "", "local":
		return &ParsedEnvURL{Local: &LocalEnv{
			ConfigPath: NormalizeConfigPath(filepath.Join(url.Host, url.Path)),
		}}, nil
	case "git":
		repo := url.Host + url.Path

		// options for git environments are set via query params
		subpath := url.Query().Get("subdir")
		subpath = NormalizeConfigPath(subpath)

		gitRef := url.Query().Get("ref")
		if gitRef == "" {
			gitRef = "main"
		}

		gitProtocol := url.Query().Get("protocol")
		if gitProtocol != "" {
			repo = gitProtocol + "://" + repo
		}

		return &ParsedEnvURL{Git: &GitEnv{
			Repo:       repo,
			Ref:        gitRef,
			ConfigPath: subpath,
		}}, nil
	default:
		return nil, fmt.Errorf("unsupported environment URL scheme: %s", url.Scheme)
	}
}

type ParsedEnvURL struct {
	// Only one of these will be set
	Local *LocalEnv
	Git   *GitEnv
}

type LocalEnv struct {
	ConfigPath string
}

type GitEnv struct {
	Repo       string
	Ref        string
	ConfigPath string
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
