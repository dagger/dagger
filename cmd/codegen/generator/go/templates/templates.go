package templates

import (
	"embed"
	"io/fs"
	"path/filepath"
	"strings"
	"text/template"
)

var (
	//go:embed all:src/*
	tmplFS embed.FS

	files map[string]*template.Template
)

func Templates(funcs template.FuncMap) map[string]*template.Template {
	if files != nil {
		for _, file := range files {
			file.Funcs(funcs)
		}
		return files
	}

	root := "src"

	tmpl := template.New("").Funcs(funcs)
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
		tmpl.AddParseTree(path, ntmpl.Tree)
		return nil
	})
	if err != nil {
		panic(err)
	}

	targets := []string{}
	err = fs.WalkDir(tmplFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), "_") {
			return fs.SkipDir
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

	files = map[string]*template.Template{}
	for _, target := range targets {
		tmpl, _ := tmpl.Clone()
		files[strings.TrimSuffix(target, ".tmpl")] = tmpl.Lookup(target)
	}
	return files
}
