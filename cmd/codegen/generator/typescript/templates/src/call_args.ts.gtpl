{{- /* Write arguments sent to method resolver. */ -}}
{{ define "call_args" }}
	{{- $maxIndex := Subtract (len .) 1 }}
	{{- range $index, $value := . }}
	    {{- if .Name | IsKeyword }}
	        {{ .Name }}:
	    {{- end  }}
		{{- .Name | FormatName }}

		{{- /* Add a ", " only if it's not the last item. */ -}}
		{{- if ne $index $maxIndex }}
			{{- "" }}, {{ "" }}
		{{- end }}
	{{- end }}
{{- end }}

