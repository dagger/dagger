{{ define "anticipatedEnums" -}}
    {{ $enums := Enums .Types }}
    {{- range $i, $type := $enums }}
{{ "" }}
        {{- template "anticipatedEnum" $type }}
    {{- end }}
{{- end }}

{{ define "anticipatedEnum" -}}
{{ "  " }}class {{ .Name | FormatName }} < T::Enum; end
{{ "" }}
{{- end }}

{{ define "enums" -}}
    {{ $enums := Enums .Types }}
    {{- range $i, $type := $enums }}
        {{- if ne $i 0 }}
{{ "" }}
        {{- end }}
        {{- template "enum" $type }}
    {{- end }}
{{ "" }}
{{- end }}

{{ define "enum" -}}
    {{- if .Description }}
        {{- /* Split comment string into a slice of one line per element. */ -}}
        {{- $desc := CommentToLines .Description }}
        {{- range $desc }}
  #{{ . }}
        {{- end }}
    {{- end }}
  class {{ .Name | FormatName }} < T::Enum
    enums do
    {{- $sortedEnumValues := SortEnumFields .EnumValues }}
    {{- range $i, $value := $sortedEnumValues }}
        {{- if $value.Description }}
            {{- /* Split comment string into a slice of one line per element. */ -}}
            {{- $desc := CommentToLines $value.Description }}
            {{- /* Add extra break line if it's not the first param. */ -}}
            {{- if ne $i 0 }}
{{""}}
            {{- end }}
            {{- range $desc }}
      #{{ . }}
            {{- end }}
        {{- end }}
      {{ $value.Name | FormatEnum }} = new('{{ $value.Name | FormatEnumValue }}')
    {{- end }}
    end
  end
{{- end }}