package templates

import (
	_ "embed"
	"text/template"
)

var (
	//go:embed src/scalar.ex.tmpl
	scalarSource string
	Scalar       *template.Template

	//go:embed src/object.ex.tmpl
	objectSource string
	Object       *template.Template

	//go:embed src/enum.ex.tmpl
	enumSource string
	Enum       *template.Template

	//go:embed src/input.ex.tmpl
	inputSource string
	Input       *template.Template
)

func init() {
	var err error

	Scalar, err = template.New("scalar").Funcs(funcMap).Parse(scalarSource)
	if err != nil {
		panic(err)
	}

	Object, err = template.New("object").Funcs(funcMap).Parse(objectSource)
	if err != nil {
		panic(err)
	}

	Enum, err = template.New("enum").Funcs(funcMap).Parse(enumSource)
	if err != nil {
		panic(err)
	}

	Input, err = template.New("input").Funcs(funcMap).Parse(inputSource)
	if err != nil {
		panic(err)
	}
}
