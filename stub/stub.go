package main

import (
	"bytes"
	"go/format"
	"strings"
	"text/template"

	"github.com/dagger/cloak/stub/templates"
)

var funcMap = template.FuncMap{
	"ToLower": strings.ToLower,
	"PascalCase": func(s string) string {
		return lintName(strings.Title(s))
	},
}

func ModelGen(pkg *Package) ([]byte, error) {
	src, err := templates.Get("model")
	if err != nil {
		return nil, err
	}
	tpl, err := template.New("model").Funcs(funcMap).Parse(string(src))
	if err != nil {
		return nil, err
	}

	var result bytes.Buffer
	err = tpl.Execute(&result, pkg)
	if err != nil {
		return nil, err
	}

	formatted, err := format.Source(result.Bytes())
	if err != nil {
		return nil, err
	}

	return formatted, nil
}

func FrontendGen(pkg *Package) ([]byte, error) {
	src, err := templates.Get("frontend")
	if err != nil {
		return nil, err
	}
	tpl, err := template.New("frontend").Funcs(funcMap).Parse(string(src))
	if err != nil {
		return nil, err
	}

	var result bytes.Buffer
	err = tpl.Execute(&result, pkg)
	if err != nil {
		return nil, err
	}

	formatted, err := format.Source(result.Bytes())
	if err != nil {
		return nil, err
	}

	return formatted, nil
}
