package templates

import (
	_ "embed"
	"text/template"
)

var (
	//go:embed src/header.go.tmpl
	headerSource string
	Header       *template.Template

	//go:embed src/scalar.go.tmpl
	scalarSource string
	Scalar       *template.Template

	//go:embed src/input.go.tmpl
	inputSource string
	Input       *template.Template

	//go:embed src/object.go.tmpl
	objectSource string
	Object       *template.Template
)

func init() {
	var err error

	Header, err = template.New("header").Funcs(funcMap).Parse(headerSource)
	if err != nil {
		panic(err)
	}
	Scalar, err = template.New("scalar").Funcs(funcMap).Parse(scalarSource)
	if err != nil {
		panic(err)
	}

	Input, err = template.New("input").Funcs(funcMap).Parse(inputSource)
	if err != nil {
		panic(err)
	}

	Object, err = template.New("object").Funcs(funcMap).Parse(objectSource)
	if err != nil {
		panic(err)
	}
}
