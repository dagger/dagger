package templates

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

var (
	//go:embed all:src/*
	tmplFS embed.FS
)

// buildTemplateTree parses every file under src/ into a single template tree
// bound to the provided FuncMap. The tree includes all files, including
// helper templates prefixed with "_" which are excluded from auto-generated
// output targets but can still be invoked by name.
func buildTemplateTree(funcs template.FuncMap) (*template.Template, error) {
	root := "src"
	base := template.New("").Funcs(funcs)

	err := fs.WalkDir(tmplFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ntmpl, err := template.New("").Funcs(funcs).ParseFS(tmplFS, path)
		if err != nil {
			return err
		}
		ntmpl = ntmpl.Lookup(filepath.Base(path))

		path = strings.TrimPrefix(path, root+"/")
		base.AddParseTree(path, ntmpl.Tree)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return base, nil
}

// Templates returns a map from output file path to a ready-to-execute
// template, one entry per non-"_"-prefixed file under src/. It is safe to
// call multiple times with different FuncMaps (e.g. for core schema vs
// per-dep schemas).
func Templates(funcs template.FuncMap) map[string]*template.Template {
	tree, err := buildTemplateTree(funcs)
	if err != nil {
		panic(err)
	}

	root := "src"
	targets := []string{}
	err = fs.WalkDir(tmplFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), "_") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		path = strings.TrimPrefix(path, root+"/")
		targets = append(targets, path)
		return nil
	})
	if err != nil {
		panic(err)
	}

	// Sort targets to ensure deterministic order
	sort.Strings(targets)

	files := map[string]*template.Template{}
	for _, target := range targets {
		files[strings.TrimSuffix(target, ".tmpl")] = tree.Lookup(target)
	}
	return files
}

// DepTemplate returns the template used to render a per-dependency generated
// file (internal/dagger/_dep.gen.go.tmpl), bound to the provided FuncMap.
// This template is intentionally excluded from the auto-generated targets
// returned by Templates() because the output file name is dynamic (derived
// from the dependency name).
func DepTemplate(funcs template.FuncMap) (*template.Template, error) {
	const name = "internal/dagger/_dep.gen.go.tmpl"
	tree, err := buildTemplateTree(funcs)
	if err != nil {
		return nil, fmt.Errorf("build template tree: %w", err)
	}
	tmpl := tree.Lookup(name)
	if tmpl == nil {
		return nil, fmt.Errorf("template %q not found in embedded filesystem", name)
	}
	return tmpl, nil
}
