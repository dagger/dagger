{{ define "object" }}
    class {{ .Name | FormatName }}
    {{ range $field := .Fields }}
        def {{ .Name | FormatName }}
                {{- if $field.TypeRef.IsScalar }}
        {{ $field.Name }} *{{ $field.TypeRef | FormatOutputType }}
        {{- end }}

        end
    {{ end -}}
    end
{{ end -}}
