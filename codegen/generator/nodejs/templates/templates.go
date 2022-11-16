package templates

import (
	"embed"
	"fmt"
	"text/template"
)

//go:embed src
var srcs embed.FS

// New creates a new template with all the template dependencies set up.
func New() *template.Template {
	topLevelTemplate := "api"
	templateDeps := []string{
		topLevelTemplate, "header", "objects", "object", "object_comment", "method", "method_solve", "call_args", "return", "return_solve", "method_comment", "types", "type", "args", "arg",
	}

	fileNames := make([]string, 0, len(templateDeps))
	for _, tmpl := range templateDeps {
		fileNames = append(fileNames, fmt.Sprintf("src/%s.ts.tmpl", tmpl))
	}

	tmpl := template.Must(template.New(topLevelTemplate).Funcs(funcMap).ParseFS(srcs, fileNames...))
	return tmpl
}
