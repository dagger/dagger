{{ $PackageName := .Name }}
package main

import (
  "github.com/dagger/cloak/dagger"

  // TODO: need more generic mechanism for generating this import
  "github.com/dagger/cloak/examples/{{ $PackageName | ToLower }}"
)

func main() {
	d := dagger.New()
	{{ range $action := .Actions }}
	d.Action("{{ $action.Name }}", func(ctx *dagger.Context, input *dagger.Input) (*dagger.Output, error) {
		typedInput := &{{ $PackageName | ToLower }}.{{ $action.Name | PascalCase }}Input{}
		if err := input.Decode(typedInput); err != nil {
			return nil, err
		}

		typedOutput := {{ $PackageName | ToLower }}.{{ $action.Name | PascalCase }}(ctx, typedInput)

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
