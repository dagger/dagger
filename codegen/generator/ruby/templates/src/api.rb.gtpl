{{ define "api" }}
	{{- template "header" -}}
{{""}}
	{{- template "types" . }}
{{""}}
	{{- template "objects" . }}
{{ end }}
