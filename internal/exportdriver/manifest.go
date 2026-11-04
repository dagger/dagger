package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/scanner"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// manifestVersion is the only manifest layout this driver reads.
const manifestVersion = 2

// manifest describes a directory of prebuilt per-package archives: the full
// output of `go tool compile -pack` (export data and object code) for each
// package, test variants included, with the settings they were compiled
// with. Packages are keyed by their ID as go list -test names them: the
// import path, or "p [p.test]" for a variant compiled for p's test.
type manifest struct {
	Version    int                      `json:"version"`
	GoVersion  string                   `json:"goVersion"`
	GOOS       string                   `json:"goos"`
	GOARCH     string                   `json:"goarch"`
	CgoEnabled bool                     `json:"cgoEnabled"`
	// Cgo, with cgo enabled, is what cgo's output depends on besides the
	// sources (cgoSettings): the archives' cgo output is good only for the
	// same.
	Cgo        map[string]string        `json:"cgo,omitempty"`
	BuildTags  []string                 `json:"buildTags"`
	Packages   map[string]manifestEntry `json:"packages"`
}

type manifestEntry struct {
	// Archive is the archive's path relative to the manifest's directory.
	Archive string `json:"archive"`
	// Sources is sourcesDigest of what the archive was compiled from.
	Sources string `json:"sources"`
	// CgoFiles, for a cgo package, are its cgo files' names, relative to
	// the package directory, and CgoOutput the Go files cgo wrote for its
	// compile, relative to the manifest's directory, in the compile's order:
	// the package compiles its other Go files and those.
	CgoFiles  []string `json:"cgoFiles,omitempty"`
	CgoOutput []string `json:"cgoOutput,omitempty"`
	// Module, for a cgo package, is the module version its files come from,
	// "path@version" (moduleVersion), or "" for the main module's or a local
	// replacement's: what its C compiles may read beyond its digest.
	Module string `json:"module,omitempty"`
}

// moduleVersion is the module version a package's files come from, the
// replacement's if any, "path@version", or "" for files no version fixes.
func moduleVersion(m *packages.Module) string {
	if m != nil && m.Replace != nil {
		m = m.Replace
	}
	if m == nil || m.Version == "" {
		return ""
	}
	return m.Path + "@" + m.Version
}

// cgoVars are the go command's settings cgo's output depends on.
var cgoVars = []string{"CC", "CXX", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_FFLAGS", "CGO_LDFLAGS"}

// cgoSettings is what cgo's output depends on besides a package's sources,
// from go env: cgoVars, and "ccVersion", the first line the C compiler
// prints for --version, or "" if it prints none.
func cgoSettings(ctx context.Context, env map[string]string) map[string]string {
	settings := map[string]string{}
	for _, v := range cgoVars {
		settings[v] = env[v]
	}
	if cc := strings.Fields(env["CC"]); len(cc) > 0 {
		out, _ := exec.CommandContext(ctx, cc[0], append(cc[1:], "--version")...).Output()
		first, _, _ := strings.Cut(string(out), "\n")
		settings["ccVersion"] = strings.TrimSpace(first)
	}
	return settings
}

func readManifest(dir string) (*manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Version != manifestVersion {
		return nil, fmt.Errorf("manifest version %d, want %d", m.Version, manifestVersion)
	}
	return &m, nil
}

// packageFiles is what an archive's export data is compiled from, as
// go/packages or go list reports it: all paths absolute.
type packageFiles struct {
	Dir             string
	GoFiles         []string // Go and cgo files
	OtherFiles      []string // C, C++, header, assembly, Fortran, Objective-C, SWIG and syso files
	EmbedFiles      []string
	CompiledGoFiles []string // the Go files compiled: GoFiles, or cgo's output
}

func filesOf(p *packages.Package) packageFiles {
	return packageFiles{p.Dir, p.GoFiles, p.OtherFiles, p.EmbedFiles, p.CompiledGoFiles}
}

// usesCgo reports whether the compiled Go files are cgo's output rather than
// the package's Go files.
func (f packageFiles) usesCgo() bool {
	if len(f.CompiledGoFiles) != len(f.GoFiles) {
		return true
	}
	gofiles := map[string]bool{}
	for _, g := range f.GoFiles {
		gofiles[g] = true
	}
	for _, c := range f.CompiledGoFiles {
		if !gofiles[c] {
			return true
		}
	}
	return false
}

// sourcesDigest identifies what a package's archive is compiled from: sha256
// over the sorted lines "<name> <sha256 of content>\n". It covers the Go
// files, the other source files and the embedded files, each named by its
// path relative to the package directory. A cgo package is compiled from
// cgo's output as well as its plain Go files, and cgo's output is what
// depends on the C toolchain, headers and flags, so it covers that output as
// well as the cgo files. The go command names cgo's output files after a
// hash of their contents, and their //line directives name the directory
// cgo ran in, so they are named by their position among the generated files
// and hashed without those directives.
func sourcesDigest(f packageFiles) (string, error) {
	var lines []string
	add := func(name, path string, generated bool) error {
		h, err := fileSHA256(path, generated)
		if err != nil {
			return err
		}
		lines = append(lines, name+" "+h+"\n")
		return nil
	}
	rel := func(path string) string {
		if r, err := filepath.Rel(f.Dir, path); err == nil {
			return filepath.ToSlash(r)
		}
		return path
	}
	if f.usesCgo() {
		lines = append(lines, "cgo\n")
		plain := map[string]bool{}
		for _, g := range f.GoFiles {
			plain[g] = true
		}
		compiled := map[string]bool{}
		for _, c := range f.CompiledGoFiles {
			compiled[c] = true
		}
		for _, g := range f.GoFiles {
			if !compiled[g] {
				if err := add(rel(g), g, false); err != nil {
					return "", err
				}
			}
		}
		generated := 0
		for _, c := range f.CompiledGoFiles {
			var err error
			if plain[c] {
				err = add(rel(c), c, false)
			} else {
				err = add(fmt.Sprintf("cgo-output-%d", generated), c, true)
				generated++
			}
			if err != nil {
				return "", err
			}
		}
	} else {
		for _, g := range f.GoFiles {
			if err := add(rel(g), g, false); err != nil {
				return "", err
			}
		}
	}
	for _, group := range [][]string{f.OtherFiles, f.EmbedFiles} {
		for _, o := range group {
			if err := add(rel(o), o, false); err != nil {
				return "", err
			}
		}
	}
	sort.Strings(lines)
	sum := sha256.New()
	for _, l := range lines {
		_, _ = io.WriteString(sum, l) // a hash never fails to write
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil)), nil
}

// fileSHA256 hashes a file's content. For a generated Go file, it leaves out
// the //line directives: the comments, as the Go scanner finds them, that
// start with "//line ".
func fileSHA256(path string, generated bool) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if !generated {
		_, _ = h.Write(data) // a hash never fails to write
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	fset := token.NewFileSet()
	file := fset.AddFile(path, -1, len(data))
	var s scanner.Scanner
	s.Init(file, data, nil, scanner.ScanComments)
	last := 0
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT && strings.HasPrefix(lit, "//line ") {
			off := file.Offset(pos)
			_, _ = h.Write(data[last:off])
			last = off + len(lit)
		}
	}
	_, _ = h.Write(data[last:])
	return hex.EncodeToString(h.Sum(nil)), nil
}

// buildTags returns the -tags value in build flags, sorted.
func buildTags(flags []string) []string {
	var tags []string
	for i := 0; i < len(flags); i++ {
		f := flags[i]
		var v string
		switch {
		case f == "-tags" || f == "--tags":
			if i+1 < len(flags) {
				v = flags[i+1]
				i++
			}
		case strings.HasPrefix(f, "-tags="):
			v = strings.TrimPrefix(f, "-tags=")
		case strings.HasPrefix(f, "--tags="):
			v = strings.TrimPrefix(f, "--tags=")
		default:
			continue
		}
		tags = tagsValue(v)
	}
	sort.Strings(tags)
	return tags
}

// tagsValue parses a -tags value as the go command does: split on commas,
// unless it has a space or a quote, which the go command allows for
// compatibility, as a quoted, space-separated list.
func tagsValue(v string) []string {
	if strings.ContainsAny(v, " '") {
		tags, err := quotedSplit(v)
		if err != nil {
			return []string{v} // go rejects it; no archives match
		}
		return tags
	}
	var tags []string
	for _, t := range strings.Split(v, ",") {
		if t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// quotedSplit splits s into fields as the go command splits GOFLAGS:
// separated by spaces, each optionally in single or double quotes, with
// nothing unescaped inside them.
func quotedSplit(s string) ([]string, error) {
	isSpace := func(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
	var fields []string
	for len(s) > 0 {
		for len(s) > 0 && isSpace(s[0]) {
			s = s[1:]
		}
		if len(s) == 0 {
			break
		}
		if s[0] == '"' || s[0] == '\'' {
			quote := s[0]
			s = s[1:]
			i := strings.IndexByte(s, quote)
			if i < 0 {
				return nil, fmt.Errorf("unterminated %c string", quote)
			}
			fields = append(fields, s[:i])
			s = s[i+1:]
			continue
		}
		i := 0
		for i < len(s) && !isSpace(s[i]) {
			i++
		}
		fields = append(fields, s[:i])
		s = s[i:]
	}
	return fields, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
