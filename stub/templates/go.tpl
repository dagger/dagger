package {{.Name | ToLower }}

import "context"

{{- range .Docs }}
// {{ . }}
{{- end }}
type {{ .Name | PascalCase }} interface {
    {{- range $action := .Actions }}
    {{- range $doc := $action.Docs }}
    // {{ $doc }}
    {{- end }}
	{{ $action.Name | PascalCase }}(context.Context, *{{ $action.Name | PascalCase }}Input) (*{{ $action.Name | PascalCase }}Output, error)
    {{ end }}
}

{{ range $action := .Actions }}
type {{ $action.Name |PascalCase }}Input struct {
    {{- range $input := $action.Inputs }}
    {{- range $doc := $input.Docs }}
    // {{ $doc }}
    {{- end }}
	{{ $input.Name | PascalCase }} {{ $input.Type }}
    {{ end }}
}

type {{ $action.Name | PascalCase }}Output struct {
    {{- range $output := $action.Outputs }}
    {{- range $doc := $output.Docs }}
    // {{ $doc }}
    {{- end }}
	{{ $output.Name | PascalCase }} {{ $output.Type }}
    {{ end }}
}
{{- end }}