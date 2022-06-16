{{ $PackageName := .Name }}
package main

import (
  "github.com/dagger/cloak/dagger"

  // TODO: this needs to be generated based on which schemas are re-used in this schema
  "github.com/dagger/cloak/dagger/core"
)

func main() {
	d := dagger.New()
	{{ range $action := .Actions }}
	d.Action("{{ $action.Name }}", func(ctx *dagger.Context, input *dagger.Input) (*dagger.Output, error) {
		typedInput := {{ $action.Name | PascalCase }}Input{}
		if err := input.Decode(&typedInput); err != nil {
			return nil, err
		}

		typedOutput, err := Do{{ $action.Name | PascalCase }}(ctx, typedInput)
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

	if err := d.Serve(); err != nil {
    panic(err)
  }
}



{{ range $action := .Actions }}

type {{ $action.Name |PascalCase }}Input struct {
    {{- range $input := $action.Inputs }}
    {{- range $doc := $input.Docs }}
    // {{ $doc }}
    {{- end }}
	{{ $input.Name | PascalCase }} {{ $input.Type }} `json:"{{ $input.Name | ToLower }},omitempty"`
    {{ end }}
}

type {{ $action.Name |PascalCase }}Output struct {
    {{- range $output := $action.Outputs }}
    {{- range $doc := $output.Docs }}
    // {{ $doc }}
    {{- end }}
	{{ $output.Name | PascalCase }} {{ $output.Type }}
    {{ end }}
}

/* TODO: need to have safe way of generating these skeletons such that we don't overwrite any existing user code in an irrecoverable way. Remember that this includes import statements too.
func Do{{ $action.Name | PascalCase }}(ctx *dagger.Context, input {{ $PackageName | ToLower }}.{{ $action.Name | PascalCase }}) (output {{ $PackageName | ToLower }}.{{ $action.Name }}Output, rerr error) {
  panic("implement me")
}
*/
{{- end }}
