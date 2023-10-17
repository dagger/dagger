{{- /* Top level template.
Composed of:
header: static template with base client and imports.
types: types, interface and input generations.
objects: types reprensetation in classes.

The additional {{""}} between each template simply insert
extra breaking line.
 */ -}}
{{ define "api" }}
	{{- template "header" }}
{{""}}
	{{- template "types" . }}
{{""}}
	{{- template "objects" . }}
{{ end }}
