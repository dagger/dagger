package model

import (
	"github.com/dagger/cloak/dagger"
)

{{- range .Docs }}
// {{ . }}
{{- end }}
type {{ .Name | PascalCase }} interface {
	{{- range $action := .Actions }}
	{{- range $doc := $action.Docs }}
	// {{ $doc }}
	{{- end }}
	{{ $action.Name | PascalCase }}(*dagger.Context, *{{ $action.Name | PascalCase }}Input) (*{{ $action.Name | PascalCase }}Output, error)
	{{ end }}
}

{{ range $action := .Actions }}
type {{ $action.Name |PascalCase }}Input struct {
	{{- range $input := $action.Inputs }}
	{{- range $doc := $input.Docs }}
	// {{ $doc }}
	{{- end }}
	{{ $input.Name | PascalCase }} {{ $input.Type }} `json:"{{ $input.Name }},omitempty"`
	{{ end }}
}

type {{ $action.Name | PascalCase }}Output struct {
	{{- range $output := $action.Outputs }}
	{{- range $doc := $output.Docs }}
	// {{ $doc }}
	{{- end }}
	{{ $output.Name | PascalCase }} {{ $output.Type }} `json:"{{ $output.Name }},omitempty"`
	{{ end }}
}
{{- end }}

func Serve(impl {{ .Name | PascalCase }}) error {
	d := dagger.New()
	{{ range $action := .Actions }}
	d.Action("{{ $action.Name }}", func(ctx *dagger.Context, input *dagger.Input) (*dagger.Output, error) {
		typedInput := &{{ $action.Name |PascalCase }}Input{}
		if err := input.Decode(typedInput); err != nil {
			return nil, err
		}

		typedOutput, err := impl.{{ $action.Name |PascalCase }}(ctx, typedInput)
		if err != nil {
			return nil, err
		}

		output := &dagger.Output{}
		if err := output.Encode(typedOutput); err != nil {
			return nil, err
		}

		return output, nil
	})
	{{- end }}

	return d.Serve()
}
