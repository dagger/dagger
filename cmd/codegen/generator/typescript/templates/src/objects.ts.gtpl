{{ define "objects" }}
	{{- range .Types }}
		{{- if HasPrefix .Name "__" }}
			{{- /* we ignore types prefixed by __ */ -}}
		{{- else }}
{{ "" }}		{{- template "object" . }}
		{{- end }}
	{{- end }}
{{- end }}
