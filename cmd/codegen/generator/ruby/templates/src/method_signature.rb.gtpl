{{- /* Write method_signature. */ -}}
{{ define "method_signature" -}}
	{{- $required := GetRequiredArgs .Args }}
	{{- $optionals := GetOptionalArgs .Args }}
	{{- if or (gt (len $required) 0) ($optionals) }}
    sig { params(
		{{- $maxReqIndex := Subtract (len $required) 1 }}
		{{- range $index, $value := $required }}
			{{- .Name | FormatArg }}: {{ .TypeRef | FormatInputType }}
			{{- if or (ne $index $maxReqIndex) ($optionals) }}, {{ end }}
		{{- end }}
		{{- if $optionals -}}
			opts: T.nilable({{ .ParentObject.Name | QueryToClient }}{{ .Name | PascalCase }}Opts)
		{{- end -}}
		).returns({{ .TypeRef | FormatOutputType }}) }
	{{- else }}
    sig { returns({{ .TypeRef | FormatOutputType }}) }
	{{- end }}
{{- end }}
