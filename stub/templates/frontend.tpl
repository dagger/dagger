{{ $PackageName := .Name }}
package main

import (
  "github.com/dagger/cloak/dagger"

  // TODO: need more generic mechanism for generating this import
  "github.com/dagger/cloak/examples/{{ $PackageName | ToLower }}/sdk/{{ $PackageName | ToLower }}"
)

func main() {
	d := dagger.New()
	{{ range $action := .Actions }}
	d.Action("{{ $action.Name }}", func(ctx *dagger.Context, input dagger.FS) (dagger.FS, error) {
		typedInput := &{{ $PackageName | ToLower }}.{{ $action.Name | PascalCase }}Input{}
		if err := dagger.Unmarshal(ctx, input, typedInput); err != nil {
			return dagger.FS{}, err
		}
		typedOutput := {{ $action.Name | PascalCase }}(ctx, typedInput)
		return dagger.Marshal(ctx, typedOutput)
	})
	{{- end }}

	if err := d.Serve(); err != nil {
    panic(err)
  }
}
