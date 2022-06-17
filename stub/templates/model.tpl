{{ $PackageName := .Name }}
package {{$PackageName | ToLower }}

import (
  "github.com/dagger/cloak/dagger"

  // TODO: this needs to be generated based on which schemas are re-used in this schema
  "github.com/dagger/cloak/dagger/core"
)

{{- range .Docs }}
// {{ . }}
{{- end }}

{{ range $action := .Actions }}
type {{ $action.Name |PascalCase }}Input struct {
    {{- range $input := $action.Inputs }}
    {{- range $doc := $input.Docs }}
    // {{ $doc }}
    {{- end }}
	{{ $input.Name | PascalCase }} {{ $input.Type }} `json:"{{ $input.Name | ToLower }},omitempty"`
    {{ end }}
}

type {{ $action.Name | PascalCase }}Output struct {
    {{- range $output := $action.Outputs }}
    {{- range $doc := $output.Docs }}
    // {{ $doc }}
    {{- end }}
	{{ $output.Name | ToLower }} {{ $output.Type }}
    {{ end }}
}

{{- range $output := $action.Outputs }}
func (a *{{ $action.Name | PascalCase }}Output) {{ $output.Name | PascalCase }}() {{ $output.Type }} {
  return a.{{ $output.Name | ToLower }}
}
{{ end }}

func {{ $action.Name | PascalCase }}(ctx *dagger.Context, input *{{ $action.Name | PascalCase }}Input) *{{ $action.Name | PascalCase }}Output {
  output := &{{ $action.Name | PascalCase }}Output{}
  if err := dagger.Do(ctx, "localhost:5555/dagger:{{ $PackageName | ToLower }}", "{{ $action.Name }}", input, output, do{{ $action.Name | PascalCase }}); err != nil {
    panic(err)
  }
  return output
}

var _ func(ctx *dagger.Context, input *{{ $action.Name | PascalCase }}Input) *{{ $action.Name | PascalCase }}Output = do{{ $action.Name | PascalCase }}

{{- end }}
