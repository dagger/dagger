package templates

import (
	_ "embed"
	"text/template"
)

var (
	//go:embed src/header.ts.tmpl
	headerSource string
	Header       *template.Template

	//go:embed src/object.ts.tmpl
	objectSource string
	Object       *template.Template
)

func init() {
	var err error

	Header, err = template.New("header").Funcs(FuncMap).Parse(headerSource)
	if err != nil {
		panic(err)
	}

	Object, err = template.New("object").Funcs(FuncMap).Parse(objectSource)
	if err != nil {
		panic(err)
	}
}
