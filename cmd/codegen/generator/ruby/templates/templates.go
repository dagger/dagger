package templates

import (
	"embed"
	"fmt"
	"text/template"
)

//go:embed src
var srcs embed.FS

// New creates a new template with all the template dependencies set up.
func New(schemaVersion string) *template.Template {
	topLevelTemplate := "api"
	templateDeps := []string{
		topLevelTemplate, "objects", "object", "method", "method_comment",
	}

	fileNames := make([]string, 0, len(templateDeps))
	for _, tmpl := range templateDeps {
		fileNames = append(fileNames, fmt.Sprintf("src/%s.rb.gtpl", tmpl))
	}

	funcs := TypescriptTemplateFuncs(schemaVersion)
	tmpl := template.Must(template.New(topLevelTemplate).Funcs(funcs).ParseFS(srcs, fileNames...))
	return tmpl
}
