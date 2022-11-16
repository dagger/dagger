package solver

import (
	"fmt"
	"strings"
)

const defaultDockerDomain = "docker.io"

// Parsing function based on splitReposSearchTerm
// "github.com/docker/docker/registry"
func ParseAuthHost(host string) (string, error) {
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimSuffix(host, "/")

	// Remove everything after @
	nameParts := strings.SplitN(host, "@", 2)
	host = nameParts[0]

	// if ":" > 1, trim after last ":" found
	if strings.Count(host, ":") > 1 {
		host = host[:strings.LastIndex(host, ":")]
	}

	// if ":" > 0, trim after last ":" found if it contains "."
	// ex: samalba/hipache:1.15, registry.com:5000:1.0
	if strings.Count(host, ":") > 0 {
		tmpStr := host[strings.LastIndex(host, ":"):]
		if strings.Count(tmpStr, ".") > 0 {
			host = host[:strings.LastIndex(host, ":")]
		}
	}

	nameParts = strings.SplitN(host, "/", 2)
	var domain string
	switch {
	// Localhost registry parsing
	case strings.Contains(nameParts[0], "localhost"):
		domain = nameParts[0]
	// If the split returned an array of len 1 that doesn't contain any .
	// ex: ubuntu
	case len(nameParts) == 1 && !strings.Contains(nameParts[0], "."):
		domain = defaultDockerDomain
	// if the split does not contain "." nor ":", but contains images
	// ex: samalba/hipache, samalba/hipache:1.15, samalba/hipache@sha:...
	case !strings.Contains(nameParts[0], ".") && !strings.Contains(nameParts[0], ":"):
		domain = defaultDockerDomain
	case nameParts[0] == "registry-1.docker.io":
		domain = defaultDockerDomain
	case nameParts[0] == "index.docker.io":
		domain = defaultDockerDomain
	// Private remaining registry parsing
	case strings.Contains(nameParts[0], "."):
		domain = nameParts[0]
	// Fail by default
	default:
		return "", fmt.Errorf("failed parsing [%s] expected host format: [%s]", nameParts[0], "registrydomain.extension")
	}
	return domain, nil
}
