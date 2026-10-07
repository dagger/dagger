{{- /* Write arguments sent to method resolver. */ -}}
{{ define "call_args" }}
	{{- $maxIndex := Subtract (len .) 1 }}
	{{- range $index, $value := . }}
	    {{- /* Key the arg by its schema name when its TS name differs. */ -}}
	    {{- if ne .Name (.Name | FormatArgName) }}
	        {{ .Name }}:
	    {{- end  }}
		{{- .Name | FormatArgName }}

		{{- /* Add a ", " only if it's not the last item. */ -}}
		{{- if ne $index $maxIndex }}
			{{- "" }}, {{ "" }}
		{{- end }}
	{{- end }}
{{- end }}

{{- /* Write optional arguments sent to method resolver: the opts, with keys
renamed in TS mapped back to their schema name. The old key, which is the
schema name, is still accepted. The dot is the optional args. */ -}}
{{ define "call_opts" }}
	{{- "" }}...opts
	{{- range RenamedArgs . }}
		{{- "" }}, {{ .Name | ArgName }}: undefined, {{ .Name }}: opts?.{{ .Name | ArgName }} ?? opts?.{{ .Name }}
	{{- end }}
{{- end }}
