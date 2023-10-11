package templates

import (
	_ "embed"
	"text/template"
)

var (
	//go:embed src/header.go.tmpl
	headerSource string
	header       *template.Template

	//go:embed src/scalar.go.tmpl
	scalarSource string
	scalar       *template.Template

	//go:embed src/input.go.tmpl
	inputSource string
	input       *template.Template

	//go:embed src/object.go.tmpl
	objectSource string
	object       *template.Template

	//go:embed src/enum.go.tmpl
	enumSource string
	enum       *template.Template

	//go:embed src/module.go.tmpl
	moduleSource string
	module       *template.Template
)

func Header(funcs template.FuncMap) *template.Template {
	if header == nil {
		var err error
		header, err = template.New("header").Funcs(funcs).Parse(headerSource)
		if err != nil {
			panic(err)
		}
	}
	return header
}

func Scalar(funcs template.FuncMap) *template.Template {
	if scalar == nil {
		var err error
		scalar, err = template.New("scalar").Funcs(funcs).Parse(scalarSource)
		if err != nil {
			panic(err)
		}
	}
	return scalar
}

func Input(funcs template.FuncMap) *template.Template {
	if input == nil {
		var err error
		input, err = template.New("input").Funcs(funcs).Parse(inputSource)
		if err != nil {
			panic(err)
		}
	}
	return input
}

func Object(funcs template.FuncMap) *template.Template {
	if object == nil {
		var err error
		object, err = template.New("object").Funcs(funcs).Parse(objectSource)
		if err != nil {
			panic(err)
		}
	}
	return object
}

func Enum(funcs template.FuncMap) *template.Template {
	if enum == nil {
		var err error
		enum, err = template.New("enum").Funcs(funcs).Parse(enumSource)
		if err != nil {
			panic(err)
		}
	}
	return enum
}

func Module(funcs template.FuncMap) *template.Template {
	if module == nil {
		var err error
		module, err = template.New("module").Funcs(funcs).Parse(moduleSource)
		if err != nil {
			panic(err)
		}
	}
	return module
}
