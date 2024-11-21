{{ define "objects" -}}
	{{- $types := ValidTypes .Types -}}
	{{- range $index, $type := $types }}
		{{- if gt $index 0 }}
{{ "" }}
{{ "" }}
		{{- end }}
		{{- template "object" $type }}
	{{- end }}
{{- end }}
