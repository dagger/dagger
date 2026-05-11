{{ define "objects" }}
	{{- range .Types }}
		{{- if HasPrefix .Name "_" }}
			{{- /* we ignore types prefixed by _ */ -}}
		{{- else if IsInterface . }}
{{ "" }}		{{- template "interface" . }}
		{{- else }}
{{ "" }}		{{- template "object" . }}
		{{- end }}
	{{- end }}
{{- end }}
