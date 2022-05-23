package analytics

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var (
	urlSchemeRegExp  = regexp.MustCompile(`^[^:]+://`)
	scpLikeURLRegExp = regexp.MustCompile(`^(?:(?P<user>[^@]+)@)?(?P<host>[^:\s]+):(?:(?P<port>[0-9]{1,5})(?:\/|:))?(?P<path>[^\\].*\/[^\\].*)$`)
)

func parseGitURL(endpoint string) (string, error) {
	if e, ok := parseSCPLike(endpoint); ok {
		return e, nil
	}

	return parseURL(endpoint)
}

func parseURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}

	if !u.IsAbs() {
		return "", fmt.Errorf(
			"invalid endpoint: %s", endpoint,
		)
	}

	return fmt.Sprintf("%s%s", u.Hostname(), strings.TrimSuffix(u.Path, ".git")), nil
}

func parseSCPLike(endpoint string) (string, bool) {
	if matchesURLScheme(endpoint) || !matchesScpLike(endpoint) {
		return "", false
	}

	_, host, _, path := findScpLikeComponents(endpoint)

	return fmt.Sprintf("%s/%s", host, strings.TrimSuffix(path, ".git")), true
}

// matchesURLScheme returns true if the given string matches a URL-like
// format scheme.
func matchesURLScheme(url string) bool {
	return urlSchemeRegExp.MatchString(url)
}

// matchesScpLike returns true if the given string matches an SCP-like
// format scheme.
func matchesScpLike(url string) bool {
	return scpLikeURLRegExp.MatchString(url)
}

// findScpLikeComponents returns the user, host, port and path of the
// given SCP-like URL.
func findScpLikeComponents(url string) (user, host, port, path string) {
	m := scpLikeURLRegExp.FindStringSubmatch(url)
	return m[1], m[2], m[3], m[4]
}
