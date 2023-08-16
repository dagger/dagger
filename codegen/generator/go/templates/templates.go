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

	//go:embed src/enum.go.tmpl
	enumSource string
	Enum       *template.Template

	//go:embed src/environment.go.tmpl
	environmentSource string
	Environment       *template.Template
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

	Enum, err = template.New("enum").Funcs(FuncMap).Parse(enumSource)
	if err != nil {
		panic(err)
	}

	Environment, err = template.New("environment").Funcs(FuncMap).Parse(environmentSource)
	if err != nil {
		panic(err)
	}
}
