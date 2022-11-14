package templates

import (
	"embed"
	_ "embed"
	"fmt"
	"text/template"
)

var (
	//go:embed src/header.ts.tmpl
	headerSource string
	Header       *template.Template

	//go:embed src/object.ts.tmpl
	objectSource string
	Object       *template.Template

	//go:embed src
	srcs embed.FS
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

// New creates a new template with all the template dependencies set up.
func New() *template.Template {
	topLevelTemplate := "api"
	templateDeps := []string{
		topLevelTemplate, "header", "objects", "object", "object_comment", "method", "method_solve", "field", "return_solve", "input_args", "return", "field_comment", "types", "type",
	}
	var fileNames []string
	for _, tmpl := range templateDeps {
		fileNames = append(fileNames, fmt.Sprintf("src/%s.ts.tmpl", tmpl))
	}

	tmpl := template.Must(template.New(topLevelTemplate).Funcs(FuncMap).ParseFS(srcs, fileNames...))
	return tmpl
}
