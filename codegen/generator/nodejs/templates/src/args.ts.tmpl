{{ define "args" }}
	{{- $parentName := .ParentObject.Name }}
	{{- $required := GetRequiredArgs .Args }}

	{{- $maxIndex := len $required }}
	{{- $maxIndex = Subtract $maxIndex 1 }}

	{{- range $index, $value := $required }}
		{{- $opt := "" -}}
		{{- if .TypeRef.IsOptional }}
			{{- $opt = "?" -}}
		{{- end }}
		{{- if and (eq .Name "id") (eq $parentName "Query") }}
			{{- .Name }}{{ $opt }}: {{ .TypeRef | FormatOutputType -}}
		{{- else }}
			{{- .Name }}{{ $opt }}: {{ .TypeRef | FormatInputType -}}
		{{- end }}

		{{- /* we add a ", " only if it's not the last item */ -}}
		{{- if ne $index $maxIndex -}}
			{{ "" }}, {{ "" }}
		{{- end }}
	{{- end }}
{{- end }}

