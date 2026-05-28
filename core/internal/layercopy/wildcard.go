//go:build linux

package layercopy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func ResolveWildcards(root, src string, followLinks bool) ([]string, error) {
	d1, d2 := splitWildcards(src)
	if d2 == "" {
		return []string{d1}, nil
	}
	p, err := rootPath(root, d1, followLinks)
	if err != nil {
		return nil, err
	}
	matches, err := resolveWildcards(p, d2)
	if err != nil {
		return nil, err
	}
	for i, m := range matches {
		p, err := filepath.Rel(root, m)
		if err != nil {
			return nil, err
		}
		matches[i] = p
	}
	return matches, nil
}

func splitWildcards(p string) (d1, d2 string) {
	parts := strings.Split(p, string(filepath.Separator))
	var p1, p2 []string
	var found bool
	for _, p := range parts {
		if !found && containsWildcards(p) {
			found = true
		}
		if p == "" {
			p = "/"
		}
		if !found {
			p1 = append(p1, p)
		} else {
			p2 = append(p2, p)
		}
	}
	return filepath.Join(p1...), filepath.Join(p2...)
}

func containsWildcards(name string) bool {
	isWindows := runtime.GOOS == "windows"
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if ch == '\\' && !isWindows {
			i++
		} else if ch == '*' || ch == '?' || ch == '[' {
			return true
		}
	}
	return false
}

func resolveWildcards(basePath, comp string) ([]string, error) {
	var out []string
	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(basePath, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if match, _ := filepath.Match(comp, rel); !match {
			return nil
		}
		out = append(out, path)
		if info.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	return out, err
}
