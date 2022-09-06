package templates

import (
	_ "embed"
	"fmt"
	"strings"
	"text/template"

	"go.dagger.io/dagger/codegen/introspection"
)

var (
	//go:embed header.go.tmpl
	headerSource string
	Header       *template.Template

	//go:embed scalar.go.tmpl
	scalarSource string
	Scalar       *template.Template

	//go:embed input.go.tmpl
	inputSource string
	Input       *template.Template

	//go:embed object.go.tmpl
	objectSource string
	Object       *template.Template
)

func formatTypeRef(r *introspection.TypeRef) string {
	ref := r
	var representation string
	for {
		switch ref.Kind {
		case introspection.TypeKindList:
			representation += "[]"
		case introspection.TypeKindScalar:
			switch introspection.Scalar(ref.Name) {
			case introspection.ScalarString:
				representation += "string"
				return representation
			case introspection.ScalarInt:
				representation += "int"
				return representation
			case introspection.ScalarBoolean:
				representation += "bool"
				return representation
			case introspection.ScalarFloat:
				representation += "float"
				return representation
			default:
				// Custom scalar
				return ref.Name
			}
		case introspection.TypeKindObject:
			representation += lintName(ref.Name)
			return representation
		case introspection.TypeKindInputObject:
			representation += lintName(ref.Name)
			return representation
		}
		ref = ref.OfType
	}
}

func formatArgs(v introspection.InputValues) string {
	representation := []string{}
	for _, i := range v {
		typ := formatTypeRef(i.TypeRef)
		representation = append(representation, fmt.Sprintf("%s %s", i.Name, typ))
	}

	return strings.Join(representation, ", ")
}

func comment(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "// " + l
	}
	return strings.Join(lines, "\n")
}

func init() {
	funcMap := template.FuncMap{
		"PascalCase": func(s string) string {
			if len(s) > 0 {
				s = strings.ToUpper(string(s[0])) + s[1:]
			}
			return lintName(s)
		},
		"FormatRef":  formatTypeRef,
		"FormatArgs": formatArgs,
		"Comment":    comment,
	}

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
