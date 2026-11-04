package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
)

// compileAll compiles every package in set with `go tool compile`, imports
// first, writing archives under work and recording them in archive. It
// returns the packages that failed to compile, or couldn't be compiled
// because an import has no archive; the error is for the driver itself
// failing, not a package.
func compileAll(ctx context.Context, env []string, work string, set map[*packages.Package]bool, archive map[*packages.Package]string, embeds map[*packages.Package]embedSet, errs map[string]string) ([]*packages.Package, error) {
	if len(set) == 0 {
		return nil, nil
	}
	var (
		mu      sync.Mutex
		done    = map[*packages.Package]bool{}
		failed  []*packages.Package
		waiting = map[*packages.Package]int{}
		users   = map[*packages.Package][]*packages.Package{}
		ready   []*packages.Package
	)
	for p := range set {
		for _, imp := range p.Imports {
			if set[imp] {
				waiting[p]++
				users[imp] = append(users[imp], p)
			}
		}
		if waiting[p] == 0 {
			ready = append(ready, p)
		}
	}
	sortByID(ready)

	jobs := make(chan *packages.Package)
	results := make(chan struct {
		p   *packages.Package
		out string
		err error
	})
	workers := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for p := range jobs {
				mu.Lock()
				cfg, ok := importcfg(p, archive)
				mu.Unlock()
				var out string
				var err error
				if !ok {
					err = fmt.Errorf("an import of %s has no archive", p.ID)
				} else {
					out, err = compileOne(ctx, env, work, p, cfg, embeds[p])
				}
				results <- struct {
					p   *packages.Package
					out string
					err error
				}{p, out, err}
			}
		})
	}

	remaining := len(set)
	inFlight := 0
	for remaining > 0 {
		for len(ready) > 0 && inFlight < workers {
			jobs <- ready[0]
			ready = ready[1:]
			inFlight++
		}
		if inFlight == 0 {
			break // what's left waits on a failed package
		}
		r := <-results
		inFlight--
		remaining--
		mu.Lock()
		done[r.p] = true
		if r.err != nil {
			failed = append(failed, r.p)
			errs[r.p.ID] = firstLine(r.err.Error())
		} else {
			archive[r.p] = r.out
		}
		mu.Unlock()
		for _, u := range users[r.p] {
			waiting[u]--
			if waiting[u] == 0 {
				ready = append(ready, u)
			}
		}
	}
	close(jobs)
	wg.Wait()
	for p := range set {
		if !done[p] {
			failed = append(failed, p)
		}
	}
	// A package whose compile succeeded against a failed import is unusable
	// too; the caller removes everything that depends on a failure.
	sortByID(failed)
	return failed, nil
}

func sortByID(ps []*packages.Package) {
	sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
}

// importcfg maps every package p transitively imports to its archive, as the
// go command does for a compile. It reports false if one has none.
func importcfg(p *packages.Package, archive map[*packages.Package]string) (string, bool) {
	var b strings.Builder
	seen := map[*packages.Package]bool{}
	byPath := map[string]*packages.Package{}
	ok := true
	var walk func(q *packages.Package)
	walk = func(q *packages.Package) {
		for _, imp := range q.Imports {
			if imp.PkgPath == "unsafe" || seen[imp] {
				continue
			}
			seen[imp] = true
			if other, dup := byPath[imp.PkgPath]; dup && other != imp {
				ok = false // two packages with one path, e.g. q and "q [p.test]"
				return
			}
			byPath[imp.PkgPath] = imp
			walk(imp)
		}
	}
	walk(p)
	srcPaths := make([]string, 0, len(p.Imports))
	for src := range p.Imports {
		srcPaths = append(srcPaths, src)
	}
	sort.Strings(srcPaths)
	for _, src := range srcPaths {
		if imp := p.Imports[src]; src != imp.PkgPath {
			fmt.Fprintf(&b, "importmap %s=%s\n", src, imp.PkgPath)
		}
	}
	paths := make([]string, 0, len(byPath))
	for pp := range byPath {
		paths = append(paths, pp)
	}
	sort.Strings(paths)
	for _, pp := range paths {
		a, has := archive[byPath[pp]]
		if !has {
			return "", false
		}
		fmt.Fprintf(&b, "packagefile %s=%s\n", pp, a)
	}
	return b.String(), ok
}

func compileOne(ctx context.Context, env []string, work string, p *packages.Package, cfg string, embeds embedSet) (string, error) {
	dir, err := os.MkdirTemp(work, "pkg-")
	if err != nil {
		return "", err
	}
	cfgPath := filepath.Join(dir, "importcfg")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		return "", err
	}
	out := filepath.Join(dir, "_pkg_.a")
	args := []string{"tool", "compile",
		"-o", out,
		"-p", p.PkgPath,
		"-lang=go" + langVersion(p.Module.GoVersion),
		"-complete",
		"-importcfg", cfgPath,
		"-pack",
	}
	if len(embeds.Patterns) > 0 {
		embed, err := embedcfg(p, embeds)
		if err != nil {
			return "", err
		}
		embedPath := filepath.Join(dir, "embedcfg")
		if err := os.WriteFile(embedPath, embed, 0o644); err != nil {
			return "", err
		}
		args = append(args, "-embedcfg", embedPath)
	}
	args = append(args, p.CompiledGoFiles...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Env = env
	cmd.Dir = p.Dir
	// The compiler reports errors on stdout.
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("compile %s: %w: %s", p.ID, err, output.String())
	}
	return out, nil
}

// embedSet is what a package embeds: its //go:embed patterns as written,
// and the files they match, as absolute paths.
type embedSet struct {
	Patterns []string
	Files    []string
}

// embedsOf is a package's embedSet as go/packages reports it, which joins
// each pattern to the package directory. For a test variant it leaves out
// the test files' patterns; see testEmbeds.
func embedsOf(p *packages.Package) (embedSet, error) {
	set := embedSet{Files: p.EmbedFiles}
	for _, abs := range p.EmbedPatterns {
		pattern, err := filepath.Rel(p.Dir, abs)
		if err != nil {
			return set, err
		}
		set.Patterns = append(set.Patterns, filepath.ToSlash(pattern))
	}
	return set, nil
}

// embedcfg writes the compiler's embed configuration: each //go:embed
// pattern with the package-relative files it matches, and where they are.
// The patterns and the matched files are reported, but not which pattern
// matched which file, so this matches them again: a pattern names a file,
// or a directory whose files (as listed) it embeds.
func embedcfg(p *packages.Package, embeds embedSet) ([]byte, error) {
	type config struct {
		Patterns map[string][]string
		Files    map[string]string
	}
	cfg := config{Patterns: map[string][]string{}, Files: map[string]string{}}
	rels := make([]string, 0, len(embeds.Files))
	for _, f := range embeds.Files {
		rel, err := filepath.Rel(p.Dir, f)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		rels = append(rels, rel)
		cfg.Files[rel] = f
	}
	for _, pattern := range embeds.Patterns {
		glob := strings.TrimPrefix(pattern, "all:")
		matched := []string{}
		for _, rel := range rels {
			if embedMatch(glob, rel) {
				matched = append(matched, rel)
			}
		}
		if len(matched) == 0 {
			return nil, fmt.Errorf("embed pattern %q of %s matches no listed file", pattern, p.ID)
		}
		cfg.Patterns[pattern] = matched
	}
	return json.Marshal(cfg)
}

// embedMatch reports whether rel, or a directory containing it, matches glob.
func embedMatch(glob, rel string) bool {
	for p := rel; p != "." && p != "/" && p != ""; p = path.Dir(p) {
		if ok, _ := path.Match(glob, p); ok {
			return true
		}
	}
	return false
}

// firstLine shortens an error to one line for the report.
func firstLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
