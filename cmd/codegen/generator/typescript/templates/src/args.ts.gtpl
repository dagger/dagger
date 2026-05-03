{{- /* Write arguments. */ -}}
{{ define "args" }}
	{{- $required := GetRequiredArgs .Args }}
	{{- $maxIndex := Subtract (len $required) 1 }}

	{{- range $index, $value := $required }}
		{{- $opt := "" }}

		{{- /* Add ? if argument is optional. */ -}}
		{{- if .TypeRef.IsOptional }}
			{{- $opt = "?" }}
		{{- end }}

		{{- .Name | FormatName }}{{ $opt }}: {{ . | FormatInputValueType }}

		{{- /* we add a ", " only if it's not the last item. */ -}}
		{{- if ne $index $maxIndex }}
			{{- "" }}, {{ "" }}
		{{- end }}
	{{- end }}
{{- end }}

