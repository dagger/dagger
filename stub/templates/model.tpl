{{ $PackageName := .Name }}
package {{$PackageName | ToLower }}

import (
  "encoding/json"
  "sync"

  "github.com/dagger/cloak/dagger"

  // TODO: this needs to be generated based on which schemas are re-used in this schema
  "github.com/dagger/cloak/dagger/core"
)

{{- range .Docs }}
// {{ . }}
{{- end }}

{{ range $action := .Actions }}
type {{ $action.Name |PascalCase }} struct {
    {{- range $input := $action.Inputs }}
    {{- range $doc := $input.Docs }}
    // {{ $doc }}
    {{- end }}
	{{ $input.Name | PascalCase }} {{ $input.Type }} `json:"{{ $input.Name | ToLower }},omitempty"`
    {{ end }}
  once sync.Once
  memo {{ $action.Name }}Output
}

{{- range $output := $action.Outputs }}
func (a *{{ $action.Name |PascalCase }}) {{ $output.Name | PascalCase }}(ctx *dagger.Context) {{ $output.Type }} {
  return a.outputOnce(ctx).{{ $output.Name | PascalCase }}
}
{{ end }}

type {{ $action.Name }}Output struct {
    {{- range $output := $action.Outputs }}
    {{- range $doc := $output.Docs }}
    // {{ $doc }}
    {{- end }}
	{{ $output.Name | PascalCase }} {{ $output.Type }}
    {{ end }}
}

type {{ $action.Name | PascalCase }}Output interface {
    {{- range $output := $action.Outputs }}
    {{- range $doc := $output.Docs }}
    // {{ $doc }}
    {{- end }}
	{{ $output.Name | PascalCase }}(ctx *dagger.Context) {{ $output.Type }}
    {{ end }}
}

func (a *{{ $action.Name |PascalCase }}) outputOnce(ctx *dagger.Context) {{ $action.Name }}Output {
  a.once.Do(func() {
		input, err := json.Marshal(a)
		if err != nil {
			panic(err)
		}
		rawOutput, err := dagger.Do(ctx, "localhost:5555/dagger:{{ $PackageName | ToLower }}", "{{ $action.Name }}", string(input))
		if err != nil {
			panic(err)
		}
		if err := rawOutput.Decode(&a.memo); err != nil {
			panic(err)
		}
  })
  return a.memo
}

{{- end }}
