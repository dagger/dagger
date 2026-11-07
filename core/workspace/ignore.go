package workspace

import (
	"path"
	"path/filepath"
	"strings"
)

// IgnorePatterns resolves dagger.toml ignore values relative to the directory
// holding configFile. Leading slashes are workspace-relative; `.` and `..` are dropped.
func IgnorePatterns(configFile string, ignore []string) []string {
	if len(ignore) == 0 {
		return nil
	}
	configDir := filepath.Dir(configFile)
	if configDir == "" {
		configDir = "."
	}
	out := make([]string, 0, len(ignore))
	for _, pattern := range ignore {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		clean, negative := strings.CutPrefix(pattern, "!")
		clean = path.Clean(strings.TrimLeft(filepath.ToSlash(clean), "/"))
		if clean == "" || clean == "." {
			continue
		}
		fromSlash := filepath.FromSlash(clean)
		if !filepath.IsLocal(fromSlash) {
			continue
		}
		resolved := filepath.ToSlash(filepath.Clean(filepath.Join(configDir, fromSlash)))
		if negative {
			resolved = "!" + resolved
		}
		out = append(out, resolved)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
