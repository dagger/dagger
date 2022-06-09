package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"strings"
	"text/template"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

type Pkg struct {
	Name        string
	Docs        []string
	Description string
	Actions     []*Action
}

type Action struct {
	Name    string
	Docs    []string
	Inputs  []*Field
	Outputs []*Field
}

type Field struct {
	Name string
	Docs []string
	Type FieldType
}

type FieldType string

const (
	FieldTypeString FieldType = "string"
	FieldTypeBool   FieldType = "bool"
)

func parse(path string) Pkg {
	f, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	cuectx := cuecontext.New()
	v := cuectx.CompileBytes(f)
	if err := v.Validate(cue.Final()); err != nil {
		panic(err)
	}

	pkg := Pkg{}
	pkg.Name, err = v.Lookup("name").String()
	if err != nil {
		panic(err)
	}
	pkg.Description, err = v.Lookup("description").String()
	if err != nil {
		panic(err)
	}
	pkg.Docs = parseDocs(v)

	actionIt, err := v.Lookup("actions").Fields()
	if err != nil {
		panic(err)
	}

	for actionIt.Next() {
		action := &Action{
			Name: actionIt.Label(),
			Docs: parseDocs(actionIt.Value()),
		}
		pkg.Actions = append(pkg.Actions, action)

		inputIt, err := actionIt.Value().Lookup("inputs").Fields()
		if err != nil {
			panic(err)
		}
		for inputIt.Next() {
			action.Inputs = append(action.Inputs, parseField(inputIt.Label(), inputIt.Value()))
		}

		outputIt, err := actionIt.Value().Lookup("outputs").Fields()
		if err != nil {
			panic(err)
		}
		for outputIt.Next() {
			action.Outputs = append(action.Outputs, parseField(outputIt.Label(), outputIt.Value()))
		}

	}

	return pkg
}

func parseField(name string, v cue.Value) *Field {
	field := &Field{
		Name: name,
		Docs: parseDocs(v),
	}

	switch t := v.IncompleteKind(); t {
	case cue.StringKind:
		field.Type = FieldTypeString
	case cue.BoolKind:
		field.Type = FieldTypeBool
	default:
		panic(t)
	}

	return field
}

func parseDocs(v cue.Value) []string {
	docs := []string{}
	for _, doc := range v.Doc() {
		for _, line := range strings.Split(doc.Text(), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			docs = append(docs, line)
		}
	}
	return docs
}

func stub(pkg Pkg) {
	funcMap := template.FuncMap{
		"ToLower": strings.ToLower,
		"PascalCase": func(s string) string {
			return lintName(strings.Title(s))
		},
	}

	src, err := os.ReadFile("go.tpl")
	if err != nil {
		panic(err)
	}
	tpl, err := template.New("go").Funcs(funcMap).Parse(string(src))
	if err != nil {
		panic(err)
	}

	var result bytes.Buffer
	err = tpl.Execute(&result, pkg)
	if err != nil {
		panic(err)
	}

	formatted, err := format.Source(result.Bytes())
	if err != nil {
		panic(err)
	}
	fmt.Println(string(formatted))

}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <file>\n", os.Args[0])
		os.Exit(1)
	}
	pkg := parse(os.Args[1])

	stub(pkg)
}
