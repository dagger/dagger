package templates

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"text/template"
)

var (
	//go:embed src/*
	tmplFS embed.FS

	files map[string]TemplateFile
)

type TemplateFile struct {
	Name string
	tmpl *template.Template
}

func (t *TemplateFile) template(name string) *template.Template {
	return t.tmpl.Lookup(fmt.Sprintf("%s.go.tmpl", name))
}

func (t *TemplateFile) Header() *template.Template {
	return t.template("header")
}

func (t *TemplateFile) Module() *template.Template {
	return t.template("module")
}

func (t *TemplateFile) Scalar() *template.Template {
	return t.template("scalar")
}

func (t *TemplateFile) Object() *template.Template {
	return t.template("object")
}

func (t *TemplateFile) Enum() *template.Template {
	return t.template("enum")
}

func (t *TemplateFile) Input() *template.Template {
	return t.template("input")
}

func TemplateFiles(funcs template.FuncMap) map[string]TemplateFile {
	if files != nil {
		for _, file := range files {
			file.tmpl.Funcs(funcs)
		}
		return files
	}

	files = map[string]TemplateFile{}
	root := "src"

	entries, err := fs.ReadDir(tmplFS, root)
	if err != nil {
		panic(err)
	}
	for _, entry := range entries {
		tmpl, err := template.New("").Funcs(funcs).ParseFS(tmplFS, filepath.Join(root, entry.Name(), "*.go.tmpl"))
		if err != nil {
			panic(err)
		}
		f := TemplateFile{Name: entry.Name(), tmpl: tmpl}
		files[f.Name] = f
	}

	return files
}
