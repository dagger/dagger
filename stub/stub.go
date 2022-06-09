package main

import (
	"bytes"
	"go/format"
	"strings"
	"text/template"

	"github.com/dagger/cloak/stub/templates"
)

func Stub(pkg *Package) ([]byte, error) {
	funcMap := template.FuncMap{
		"ToLower": strings.ToLower,
		"PascalCase": func(s string) string {
			return lintName(strings.Title(s))
		},
	}

	src, err := templates.Get("go")
	if err != nil {
		return nil, err
	}
	tpl, err := template.New("go").Funcs(funcMap).Parse(string(src))
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
