package templates

import (
	_ "embed"
	"text/template"
)

var (
	//go:embed src/header.ts.tmpl
	headerSource string
	Header       *template.Template

	//go:embed src/scalar.ts.tmpl
	scalarSource string
	Scalar       *template.Template

	//go:embed src/input.ts.tmpl
	inputSource string
	Input       *template.Template

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
	Scalar, err = template.New("scalar").Funcs(FuncMap).Parse(scalarSource)
	if err != nil {
		panic(err)
	}

	Input, err = template.New("input").Funcs(FuncMap).Parse(inputSource)
	if err != nil {
		panic(err)
	}

	Object, err = template.New("object").Funcs(FuncMap).Parse(objectSource)
	if err != nil {
		panic(err)
	}
}
