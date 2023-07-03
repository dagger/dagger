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
	var fileNames []string
	for _, tmpl := range []string{"api", "header", "objects", "object", "method", "method_solve", "call_args", "method_comment", "types", "args"} {
		fileNames = append(fileNames, fmt.Sprintf("src/%s.rb.gtpl", tmpl))
	}
	return template.Must(template.New("api").Funcs(funcMap).ParseFS(srcs, fileNames...))
}
