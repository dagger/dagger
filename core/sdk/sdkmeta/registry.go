package sdkmeta

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SDK installation and migration share this registry of names and aliases.

//go:embed sdks.json
var embeddedSDKRegistry []byte

// SDKEntry identifies an SDK and its accepted short names.
type SDKEntry struct {
	Name        string   `json:"name"`        // canonical user-facing SDK name (e.g., "go")
	Description string   `json:"description"` // user-facing search description
	Repo        string   `json:"repo"`        // canonical full ref (e.g., "github.com/dagger/go-sdk")
	Aliases     []string `json:"aliases"`     // user-facing shorthands (e.g., ["golang"])
}

// LoadRegistry returns the embedded SDK registry.
func LoadRegistry() ([]SDKEntry, error) {
	return ParseRegistry(embeddedSDKRegistry)
}

// ParseRegistry decodes an SDK registry.
func ParseRegistry(data []byte) ([]SDKEntry, error) {
	var entries []SDKEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse SDK registry: %w", err)
	}
	return entries, nil
}

// Resolve maps a user-supplied SDK install value to the canonical full ref
// that should flow downstream to the engine.
//
// Resolution rules:
//   - If input contains `/` or `@`, treat as a full ref. Return as-is.
//   - Otherwise look up in sdks.json by name first, then alias, then repo
//     basename as a compatibility fallback (`go-sdk`).
//   - 0 matches → error ("not found").
//   - 1 match  → return entry.Repo.
//   - N > 1   → error ("ambiguous"), with candidate names.
//
// Registry names and aliases affect the default install name and the
// SDK name persisted under [sdks.<name>]. Only canonical full refs
// flow past this function.
func Resolve(input string) (string, error) {
	ref, _, _, err := ResolveInstall(input)
	return ref, err
}

// ResolveInstall maps an SDK install value to:
//   - the canonical full ref that should flow downstream to the engine; and
//   - the workspace install name to use when the user did not pass --name: the
//     registry repo basename with a "dagger-" prefix (e.g. "dagger-go-sdk"),
//     reducing the chance of colliding with an unrelated module; and
//   - the registry's canonical user-facing SDK name.
//
// Full refs return an empty install name so Workspace.withSDK keeps its normal
// basename-derived behavior for third-party SDK refs. They also return an
// empty SDK name so third-party refs can rely on the conventional name derived
// from the module entry name.
func ResolveInstall(input string) (ref string, installName string, sdkName string, _ error) {
	if strings.ContainsAny(input, "/@") {
		return input, "", "", nil
	}
	entries, err := LoadRegistry()
	if err != nil {
		return "", "", "", err
	}
	var matches []SDKEntry
	for _, e := range entries {
		if e.Name == input {
			matches = append(matches, e)
			continue
		}
		matched := false
		for _, alias := range e.Aliases {
			if alias == input {
				matches = append(matches, e)
				matched = true
				break
			}
		}
		if !matched && sdkRegistryRepoBase(e.Repo) == input {
			matches = append(matches, e)
		}
	}
	switch len(matches) {
	case 0:
		return "", "", "", fmt.Errorf("SDK %q not found in registry; try `dagger module search %s` or pass a full ref (e.g., github.com/dagger/go-sdk)", input, input)
	case 1:
		return matches[0].Repo, InstallNamePrefix + sdkRegistryRepoBase(matches[0].Repo), matches[0].Name, nil
	default:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, m.Name)
		}
		sort.Strings(names)
		return "", "", "", fmt.Errorf("SDK %q is ambiguous: matches %s; pick one", input, strings.Join(names, ", "))
	}
}

func sdkRegistryRepoBase(repo string) string {
	if idx := strings.LastIndex(repo, "/"); idx != -1 {
		return repo[idx+1:]
	}
	return repo
}
