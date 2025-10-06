{{- /* Write arguments. */ -}}
{{ define "args" }}
	{{- $parentName := .ParentObject.Name | FormatName }}
	{{- $required := GetRequiredArgs .Args }}
	{{- $maxIndex := Subtract (len $required) 1 }}

	{{- range $index, $value := $required }}
		{{- $opt := "" }}

		{{- /* Add ? if argument is optional. */ -}}
		{{- if .TypeRef.IsOptional }}
			{{- $opt = "?" }}
		{{- end }}

		{{- if and (eq .Name "id") (eq $parentName "Query") }}
			{{- .Name | FormatName }}{{ $opt }}: {{ .TypeRef | FormatOutputType }}
		{{- else }}
			{{- .Name | FormatName }}{{ $opt }}: {{ .TypeRef | FormatInputType }}
		{{- end }}

		{{- /* we add a ", " only if it's not the last item. */ -}}
		{{- if ne $index $maxIndex }}
			{{- "" }}, {{ "" }}
		{{- end }}
	{{- end }}
{{- end }}

