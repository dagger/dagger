{{- /* Write method signature using Sorbet. */ -}}
{{ define "method_signature" -}}
	{{- $required := GetRequiredArgs .Args }}
	{{- $optionals := GetOptionalArgs .Args }}
	{{- $argsDesc := ArgsHaveDescription .Args }}
	{{- $deprecationLines := FormatDeprecation .DeprecationReason }}
    {{ "sig { " }}
	{{- if gt (len .Args) 0 }}params({{ end }}
	{{- $maxReqIndex := Subtract (len $required) 1 }}
	{{- range $index, $value := $required }}
		{{- .Name | FormatArg }}: {{ .TypeRef | FormatInputType }}
		{{- if ne $index $maxReqIndex }}, {{ end }}
	{{- end }}
	{{- if and $required $optionals }}, {{end}}
	{{- if $optionals }}opts: T.nilable({{ .ParentObject.Name | QueryToClient }}{{ .Name | PascalCase }}Opts)
	{{- end }}
	{{- if gt (len .Args) 0 }}).{{ end -}}
returns({{ .TypeRef | FormatOutputType }}) }
{{ end }}
