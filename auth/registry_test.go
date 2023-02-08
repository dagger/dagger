package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAuthAddress(t *testing.T) {
	type TestCase struct {
		InputAddress string
		Expected     string
	}

	testCases := map[string]TestCase{
		// Short
		"Short address":                    {"foo", "docker.io"},
		"Short address with tag":           {"foo:1.1", "docker.io"},
		"Short address with sha":           {"foo@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "docker.io"},
		"Short address with image":         {"foo/bar", "docker.io"},
		"Short address with image and tag": {"foo/bar:1.1", "docker.io"},
		"Short address with image sha":     {"foo/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "docker.io"},

		// Private registry
		"Private registry address":                             {"registry.com", "registry.com"},
		"Private registry address with tag":                    {"registry.com:1.1", "registry.com"},
		"Private registry address with sha":                    {"registry.com@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "registry.com"},
		"Private registry address with image":                  {"registry.com/bar", "registry.com"},
		"Private registry address with image and tag":          {"registry.com/bar:1.1", "registry.com"},
		"Private registry address with image sha":              {"registry.com/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "registry.com"},
		"Private registry address with port":                   {"registry.com:5000", "registry.com:5000"},
		"Private registry address with port and tag":           {"registry.com:5000:1.1", "registry.com:5000"},
		"Private registry address with port and sha":           {"registry.com:5000@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "registry.com:5000"},
		"Private registry address with port and image":         {"registry.com:5000/bar", "registry.com:5000"},
		"Private registry address with port and image and tag": {"registry.com:5000/bar:1.1", "registry.com:5000"},
		"Private registry address with port and image sha":     {"registry.com:5000/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "registry.com:5000"},

		// Docker.io related
		"Short docker.io":                         {"docker.io", "docker.io"},
		"Short docker.io with tag":                {"docker.io:1.1", "docker.io"},
		"Short docker.io with sha":                {"docker.io@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "docker.io"},
		"Short docker.io with image":              {"docker.io/bar", "docker.io"},
		"Short docker.io with image and tag":      {"docker.io/bar:1.1", "docker.io"},
		"Short docker.io with image sha":          {"docker.io/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "docker.io"},
		"registry-1.docker.io":                    {"docker.io", "docker.io"},
		"registry-1.docker.io with tag":           {"docker.io:1.1", "docker.io"},
		"registry-1.docker.io with sha":           {"docker.io@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "docker.io"},
		"registry-1.docker.io with image":         {"docker.io/bar", "docker.io"},
		"registry-1.docker.io with image and tag": {"docker.io/bar:1.1", "docker.io"},
		"registry-1.docker.io with image sha":     {"docker.io/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "docker.io"},
		"index.docker.io":                         {"docker.io", "docker.io"},
		"index.docker.io with tag":                {"docker.io:1.1", "docker.io"},
		"index.docker.io with sha":                {"docker.io@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "docker.io"},
		"index.docker.io with image":              {"docker.io/bar", "docker.io"},
		"index.docker.io with image and tag":      {"docker.io/bar:1.1", "docker.io"},
		"index.docker.io with image sha":          {"docker.io/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "docker.io"},

		// localhost
		"localhost registry address":                             {"localhost", "localhost"},
		"localhost registry address with tag":                    {"localhost:1.1", "localhost"},
		"localhost registry address with sha":                    {"localhost@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "localhost"},
		"localhost registry address with image":                  {"localhost/bar", "localhost"},
		"localhost registry address with image and tag":          {"localhost/bar:1.1", "localhost"},
		"localhost registry address with image sha":              {"localhost/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "localhost"},
		"localhost registry address with port":                   {"localhost:5000", "localhost:5000"},
		"localhost registry address with port and tag":           {"localhost:5000:1.1", "localhost:5000"},
		"localhost registry address with port and sha":           {"localhost:5000@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "localhost:5000"},
		"localhost registry address with port and image":         {"localhost:5000/bar", "localhost:5000"},
		"localhost registry address with port and image and tag": {"localhost:5000/bar:1.1", "localhost:5000"},
		"localhost registry address with port and image sha":     {"localhost:5000/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "localhost:5000"},

		// No host
		"Empty string":       {"", "docker.io"},
		"Only image":         {"/test", "docker.io"},
		"Only image and tag": {"/test:5.0", "docker.io"},

		// Cloud provider registry
		"AWS":                    {"https://123456789012.dkr.ecr.us-west-1.amazonaws.com", "123456789012.dkr.ecr.us-west-1.amazonaws.com"},
		"AWS with tag":           {"https://123456789012.dkr.ecr.us-west-1.amazonaws.com:1.1", "123456789012.dkr.ecr.us-west-1.amazonaws.com"},
		"AWS with sha":           {"https://123456789012.dkr.ecr.us-west-1.amazonaws.com@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "123456789012.dkr.ecr.us-west-1.amazonaws.com"},
		"AWS with image":         {"https://123456789012.dkr.ecr.us-west-1.amazonaws.com/bar", "123456789012.dkr.ecr.us-west-1.amazonaws.com"},
		"AWS with image and tag": {"https://123456789012.dkr.ecr.us-west-1.amazonaws.com/bar:1.1", "123456789012.dkr.ecr.us-west-1.amazonaws.com"},
		"AWS with image sha":     {"https://123456789012.dkr.ecr.us-west-1.amazonaws.com/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "123456789012.dkr.ecr.us-west-1.amazonaws.com"},

		"GitHub":                    {"ghcr.io", "ghcr.io"},
		"GitHub with tag":           {"ghcr.io:1.1", "ghcr.io"},
		"GitHub with sha":           {"ghcr.io@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "ghcr.io"},
		"GitHub with image":         {"ghcr.io/bar", "ghcr.io"},
		"GitHub with image and tag": {"ghcr.io/bar:1.1", "ghcr.io"},
		"GitHub with image sha":     {"ghcr.io/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", "ghcr.io"},
	}

	for name, tt := range testCases {
		t.Run(name, func(t *testing.T) {
			result, err := parseAuthAddress(tt.InputAddress)
			require.NoError(t, err)
			require.Equalf(t, tt.Expected, result, "Invalid sanitization reference for [%q]. Expected [%s] but got [%s]", name, tt.Expected, tt.InputAddress)
		})
	}
}
