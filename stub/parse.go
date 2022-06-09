package main

import (
	"os"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

const schemaSource = `
#Schema: {
	name:        string
	description: string

	actions: [string]: {
		inputs: [string]: _

		outputs: [string]: _
	}
}
`

type Package struct {
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

func Parse(path string) (*Package, error) {
	cuectx := cuecontext.New()

	schema := cuectx.CompileString(schemaSource)
	if err := schema.Validate(cue.Final()); err != nil {
		panic(err)
	}

	f, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	v := cuectx.CompileBytes(f)
	if err := v.Validate(cue.Final()); err != nil {
		return nil, err
	}

	v = schema.LookupDef("#Schema").Unify(v)
	if err := v.Err(); err != nil {
		return nil, err
	}

	pkg := &Package{}
	pkg.Name, err = v.Lookup("name").String()
	if err != nil {
		return nil, err
	}
	pkg.Description, err = v.Lookup("description").String()
	if err != nil {
		return nil, err
	}
	pkg.Docs = parseDocs(v)

	actionIt, err := v.Lookup("actions").Fields()
	if err != nil {
		return nil, err
	}

	for actionIt.Next() {
		action := &Action{
			Name: actionIt.Label(),
			Docs: parseDocs(actionIt.Value()),
		}
		pkg.Actions = append(pkg.Actions, action)

		inputIt, err := actionIt.Value().Lookup("inputs").Fields()
		if err != nil {
			return nil, err
		}
		for inputIt.Next() {
			action.Inputs = append(action.Inputs, parseField(inputIt.Label(), inputIt.Value()))
		}

		outputIt, err := actionIt.Value().Lookup("outputs").Fields()
		if err != nil {
			return nil, err
		}
		for outputIt.Next() {
			action.Outputs = append(action.Outputs, parseField(outputIt.Label(), outputIt.Value()))
		}

	}

	return pkg, nil
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
