{{ define "objects" }}
	{{- range .Types }}
		{{- if HasPrefix .Name "_" }}
			{{- /* we ignore types prefixed by _ */ -}}
		{{- else }}
{{ "" }}		{{- template "object" . }}
		{{- end }}
	{{- end }}
{{- end }}
