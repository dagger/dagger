{{ define "objects" }}
module Dagger
	{{- range . }}
		{{- if HasPrefix .Name "__" }}
			{{- /* we ignore types prefixed by __ */ -}}
		{{- else }}
			{{- template "object" . }}
		{{- end }}
	{{- end -}}
end
{{- end }}
