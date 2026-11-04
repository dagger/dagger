package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// assignExportData sets ExportFile on every package golangci-lint may load
// from export data, as go list -export does: every package but unsafe and
// the generated test mains, which golangci-lint skips. It type-checks the
// packages it lints from source, but it lints a package with internal tests
// through its test variant and loads the plain package from export data, and
// it doesn't lint a package whose findings it has cached, which it loads from
// export data when a package it does analyze imports it.
//
// Each package gets, in order of preference:
//  1. its prebuilt archive, test variants included, if the archive's sources
//     match the package's and every package it imports also uses its
//     prebuilt archive;
//  2. an archive this driver compiles with `go tool compile`, against the
//     archives of its imports, for a package without non-Go files that has
//     no matching prebuilt archive;
//  3. the export data `go list -export` builds, as without the driver.
func assignExportData(ctx context.Context, cfg *packages.Config, m *manifest, dir string, roots []*packages.Package, rep *report) error {
	var all []*packages.Package
	packages.Visit(roots, nil, func(p *packages.Package) { all = append(all, p) })

	// Errors on packages before this driver adds any from go list.
	listed := map[*packages.Package]int{}
	for _, p := range all {
		listed[p] = len(p.Errors)
	}

	// archive holds every package's chosen archive, roots included: a
	// compiled package needs its imports' archives even when they are roots.
	archive := map[*packages.Package]string{}

	verified := map[*packages.Package]bool{}
	var verify func(p *packages.Package) bool
	verify = func(p *packages.Package) bool {
		if ok, seen := verified[p]; seen {
			return ok
		}
		verified[p] = false // no import cycles in a valid graph
		ok := verifyPrebuilt(m, dir, p)
		for _, imp := range p.Imports {
			if imp.PkgPath != "unsafe" && !verify(imp) {
				ok = false
			}
		}
		verified[p] = ok
		return ok
	}
	for _, p := range all {
		if p.PkgPath != "unsafe" && verify(p) {
			archive[p] = absArchive(dir, m.Packages[p.ID].Archive)
		}
	}

	// needed: every package whose archive must exist, so the caller can load
	// it or this driver can compile one that imports it.
	needed := map[*packages.Package]bool{}
	var need func(p *packages.Package)
	need = func(p *packages.Package) {
		if p.PkgPath == "unsafe" || needed[p] {
			return
		}
		needed[p] = true
		if _, ok := archive[p]; ok {
			return // a verified archive needs nothing further
		}
		if compilable(p) {
			for _, imp := range p.Imports {
				need(imp)
			}
		}
	}
	served := func(p *packages.Package) bool {
		return p.PkgPath != "unsafe" && !isTestMain(p)
	}
	// Root test packages (test variants and external tests) are served too.
	// golangci-lint type-checks those it lints from source, which misses
	// errors only the compiler reports, such as //go:embed on a variable of
	// the wrong type or a function without a body. go list -export compiles
	// them all, so this driver does too: those that fail get go list's error.
	for _, p := range all {
		if served(p) {
			need(p)
		}
	}

	compileSet := map[*packages.Package]bool{}
	var golist []*packages.Package
	for p := range needed {
		if _, ok := archive[p]; ok {
			continue
		}
		// A caller that type-checks every package without export data from
		// source, as go/packages does, gets no archive for it instead.
		if os.Getenv(envCompile) == "never" {
			continue
		}
		if compilable(p) {
			compileSet[p] = true
		} else {
			golist = append(golist, p)
		}
	}

	if len(golist) > 0 {
		if err := goListExport(ctx, cfg, golist, archive); err != nil {
			return err
		}
	}
	work, err := workDir()
	if err != nil {
		return err
	}
	embeds, err := embedsFor(ctx, cfg, compileSet)
	if err != nil {
		return err
	}
	compileErrs := map[string]string{}
	failed, err := compileAll(ctx, cfg.Env, work, compileSet, archive, embeds, compileErrs)
	if err != nil {
		return err
	}
	if len(failed) > 0 {
		// A package this driver couldn't compile, and everything compiled
		// against it, falls back to the go command.
		for p := range dependentsWithin(failed, compileSet) {
			delete(archive, p)
			golist = append(golist, p)
			delete(compileSet, p)
		}
		var redo []*packages.Package
		for _, p := range golist {
			if _, ok := archive[p]; !ok {
				redo = append(redo, p)
			}
		}
		if err := goListExport(ctx, cfg, redo, archive); err != nil {
			return err
		}
	}

	for _, r := range roots {
		if r.ID != r.PkgPath && !isTestMain(r) && archive[r] != "" {
			rep.TestsCompiled++
		}
	}
	for _, p := range all {
		if !served(p) {
			continue
		}
		p.ExportFile = archive[p]
		switch {
		case p.ExportFile == "":
			rep.Missing = append(rep.Missing, p.ID)
		case compileSet[p]:
			rep.Compiled++
		case verified[p]:
			rep.Prebuilt++
		default:
			rep.GoList++
		}
	}
	sort.Strings(rep.Missing)

	// A plain package that failed to compile fails in its internal test
	// variant too, which compiles the same files and which golangci-lint
	// lints in its place. Get that error from go list, as without the
	// driver: it names the variant.
	failedPlain := map[string]bool{}
	for _, p := range all {
		if p.ID == p.PkgPath && len(p.Errors) > listed[p] {
			failedPlain[p.PkgPath] = true
		}
	}
	var variants []*packages.Package
	for _, r := range roots {
		if r.ID != r.PkgPath && failedPlain[r.PkgPath] {
			variants = append(variants, r)
		}
	}
	if len(variants) > 0 {
		if err := goListExport(ctx, cfg, variants, map[*packages.Package]string{}); err != nil {
			return err
		}
	}
	for _, id := range keys(boolKeys(compileErrs)) {
		rep.CompileErrors = append(rep.CompileErrors, id+": "+compileErrs[id])
	}
	return nil
}

// verifyPrebuilt reports whether the manifest has an archive for p, by its
// ID, test variants included, compiled from p's sources as they are.
func verifyPrebuilt(m *manifest, dir string, p *packages.Package) bool {
	entry, ok := m.Packages[p.ID]
	if !ok || entry.Archive == "" || entry.Sources == "" {
		return false
	}
	if len(p.Errors) > 0 {
		return false
	}
	digest, err := sourcesDigest(filesOf(p))
	if err != nil || digest != entry.Sources {
		return false
	}
	// An archive the manifest lists but that isn't there is no archive.
	info, err := os.Stat(absArchive(dir, entry.Archive))
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// compilable reports whether this driver compiles the package itself: a
// package of plain Go files outside the standard library. Packages with cgo,
// assembly or other non-Go files, and standard library packages, which the
// compiler treats specially, are left to the go command.
func compilable(p *packages.Package) bool {
	if p.Module == nil || len(p.OtherFiles) > 0 || len(p.Errors) > 0 {
		return false
	}
	if len(p.CompiledGoFiles) == 0 || len(p.CompiledGoFiles) != len(p.GoFiles) {
		return false // cgo-processed files differ from the Go files
	}
	return true
}

// dependentsWithin returns the failed packages and every package in set that
// imports one of them, directly or not.
func dependentsWithin(failed []*packages.Package, set map[*packages.Package]bool) map[*packages.Package]bool {
	out := map[*packages.Package]bool{}
	for _, p := range failed {
		out[p] = true
	}
	for changed := true; changed; {
		changed = false
		for p := range set {
			if out[p] {
				continue
			}
			for _, imp := range p.Imports {
				if out[imp] {
					out[p] = true
					changed = true
					break
				}
			}
		}
	}
	return out
}

// goListExport asks the go command for the export data of pkgs, as
// go/packages' own driver does. Test variants are listed through their test's
// package, the only way go list names them.
func goListExport(ctx context.Context, cfg *packages.Config, pkgs []*packages.Package, archive map[*packages.Package]string) error {
	if len(pkgs) == 0 {
		return nil
	}
	byID := map[string]*packages.Package{}
	plain := map[string]bool{}
	tests := map[string]bool{}
	for _, p := range pkgs {
		byID[p.ID] = p
		if forTest, ok := testOf(p.ID); ok {
			tests[forTest] = true
		} else {
			plain[p.PkgPath] = true
		}
	}
	if len(plain) > 0 {
		if err := goList(ctx, cfg, false, keys(plain), byID, archive); err != nil {
			return err
		}
	}
	if len(tests) > 0 {
		if err := goList(ctx, cfg, true, keys(tests), byID, archive); err != nil {
			return err
		}
	}
	return nil
}

// testOf returns p for a test variant ID "q [p.test]".
func testOf(id string) (string, bool) {
	i := strings.Index(id, " [")
	if i < 0 || !strings.HasSuffix(id, ".test]") {
		return "", false
	}
	return strings.TrimSuffix(id[i+2:], ".test]"), true
}

func goList(ctx context.Context, cfg *packages.Config, tests bool, paths []string, byID map[string]*packages.Package, archive map[*packages.Package]string) error {
	args := []string{"list", "-e", "-export", "-json=ImportPath,Export,Error"}
	if tests {
		args = append(args, "-test", "-deps")
	}
	args = append(args, cfg.BuildFlags...)
	args = append(args, "--")
	args = append(args, paths...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Env = cfg.Env
	cmd.Dir = cfg.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	dec := json.NewDecoder(&stdout)
	for {
		var lp struct {
			ImportPath, Export string
			Error              *struct{ Pos, Err string }
		}
		if err := dec.Decode(&lp); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("go list output: %w", err)
		}
		p, ok := byID[lp.ImportPath]
		if !ok {
			continue
		}
		if lp.Export != "" {
			archive[p] = lp.Export
		} else if lp.Error != nil {
			// Report why there is no export data, such as a compile error
			// go/types doesn't catch, as go/packages' go list driver does.
			addError(p, packages.Error{Pos: lp.Error.Pos, Msg: strings.TrimSpace(lp.Error.Err), Kind: packages.ListError})
		}
	}
	return nil
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func boolKeys(m map[string]string) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func addError(p *packages.Package, e packages.Error) {
	for _, have := range p.Errors {
		if have.Msg == e.Msg && have.Pos == e.Pos {
			return
		}
	}
	p.Errors = append(p.Errors, e)
}

// isTestMain reports whether p is a generated test main package "p.test",
// which golangci-lint doesn't lint.
func isTestMain(p *packages.Package) bool {
	return p.Name == "main" && strings.HasSuffix(p.PkgPath, ".test")
}

// embedsFor returns what each package to compile embeds. go/packages leaves
// the test files' patterns out of a test variant's, so for test packages
// they come from go list: an internal test variant "p [p.test]" embeds p's
// and its test files' patterns, an external test package p_test its own.
func embedsFor(ctx context.Context, cfg *packages.Config, set map[*packages.Package]bool) (map[*packages.Package]embedSet, error) {
	out := map[*packages.Package]embedSet{}
	tests := map[string]bool{}
	for p := range set {
		if forTest, ok := testOf(p.ID); ok {
			tests[forTest] = true
			continue
		}
		e, err := embedsOf(p)
		if err != nil {
			return nil, err
		}
		out[p] = e
	}
	if len(tests) == 0 {
		return out, nil
	}
	// -test makes go list resolve the test files' patterns to files.
	args := append([]string{"list", "-e", "-test",
		"-json=ImportPath,Dir,EmbedPatterns,EmbedFiles,TestEmbedPatterns,TestEmbedFiles,XTestEmbedPatterns,XTestEmbedFiles"},
		cfg.BuildFlags...)
	args = append(args, "--")
	args = append(args, keys(tests)...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Env = cfg.Env
	cmd.Dir = cfg.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	type listed struct {
		ImportPath, Dir                     string
		EmbedPatterns, EmbedFiles           []string
		TestEmbedPatterns, TestEmbedFiles   []string
		XTestEmbedPatterns, XTestEmbedFiles []string
	}
	byPath := map[string]listed{}
	dec := json.NewDecoder(&stdout)
	for {
		var lp listed
		if err := dec.Decode(&lp); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list output: %w", err)
		}
		byPath[lp.ImportPath] = lp
	}
	abs := func(dir string, files ...[]string) []string {
		var all []string
		for _, group := range files {
			for _, f := range group {
				all = append(all, filepath.Join(dir, f))
			}
		}
		return all
	}
	for p := range set {
		forTest, ok := testOf(p.ID)
		if !ok {
			continue
		}
		lp := byPath[forTest]
		switch p.PkgPath {
		case forTest:
			out[p] = embedSet{
				Patterns: append(append([]string{}, lp.EmbedPatterns...), lp.TestEmbedPatterns...),
				Files:    abs(lp.Dir, lp.EmbedFiles, lp.TestEmbedFiles),
			}
		case forTest + "_test":
			out[p] = embedSet{Patterns: lp.XTestEmbedPatterns, Files: abs(lp.Dir, lp.XTestEmbedFiles)}
		default:
			// A package recompiled for the test, "q [p.test]": its own files.
			e, err := embedsOf(p)
			if err != nil {
				return nil, err
			}
			out[p] = e
		}
	}
	return out, nil
}
