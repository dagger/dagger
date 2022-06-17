{{ $PackageName := .Name }}
package {{$PackageName | ToLower }}

import (
  "encoding/json"

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
	{{ $output.Name | PascalCase }} {{ $output.Type }}
    {{ end }}
}

func {{ $action.Name | PascalCase }}(ctx *dagger.Context, input *{{ $action.Name | PascalCase }}Input) *{{ $action.Name | PascalCase }}Output {
	rawInput, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}

  rawOutput, err := dagger.Do(ctx, "localhost:5555/dagger:{{ $PackageName | ToLower }}", "{{ $action.Name }}", string(rawInput))
  if err != nil {
    panic(err)
  }
  output := &{{ $action.Name | PascalCase }}Output{}
	if err := rawOutput.Decode(output); err != nil {
		panic(err)
	}
  return output
}
{{- end }}
