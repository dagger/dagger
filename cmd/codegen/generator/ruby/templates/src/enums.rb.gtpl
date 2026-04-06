{{- /* Enums generation. */ -}}
{{ define "enums" -}}
	{{ $enums := Enums .Types }}
	{{- range $i, $type := $enums }}
		{{- if ne $i 0 }}
{{ "" }}
		{{- end }}
		{{- template "enum" $type }}
	{{- end }}
{{- end }}

{{ define "enum" -}}
	{{- if .Description }}
		{{- $desc := CommentToLines .Description }}
		{{- range $desc }}
  #{{ . }}
		{{- end }}
	{{- end }}
  class {{ .Name }} < T::Enum
    enums do
	{{- range SortEnumFields .EnumValues }}
		{{- if .Description }}
			{{- $desc := CommentToLines .Description }}
			{{- range $desc }}
      #{{ . }}
			{{- end }}
		{{- end }}
		{{- if .IsDeprecated }}
			{{- $desc := FormatDeprecation .DeprecationReason }}
			{{- range $desc }}
      #{{ . }}
			{{- end }}
		{{- end }}
      {{ .Name | FormatEnumValue }} = new('{{ .Name }}')
	{{- end }}
    end
  end
{{ "" }}
{{- end }}
