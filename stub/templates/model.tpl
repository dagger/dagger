{{ $PackageName := .Name }}
package {{$PackageName | ToLower }}

import (
  "github.com/dagger/cloak/dagger"
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
	{{ $output.Name | PascalCase }} {{ $output.Type }} `json:"{{ $output.Name | ToLower }},omitempty"`
    {{ end }}
}

func {{ $action.Name | PascalCase }}(ctx *dagger.Context, input *{{ $action.Name | PascalCase }}Input) *{{ $action.Name | PascalCase }}Output {
	fsInput, err := dagger.Marshal(ctx, input)
	if err != nil {
		panic(err)
	}

  fsOutput, err := dagger.Do(ctx, "localhost:5555/dagger:{{ $PackageName | ToLower }}", "{{ $action.Name }}", fsInput)
  if err != nil {
    panic(err)
  }
  output := &{{ $action.Name | PascalCase }}Output{}
	if err := dagger.Unmarshal(ctx, fsOutput, output); err != nil {
		panic(err)
	}
  return output
}
{{- end }}
