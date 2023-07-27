{{- /* Generate class from GraphQL struct query type. */ -}}
{{ define "object" }}
	{{- with . }}
		{{- if .Fields }}

			{{- /* Write description. */ -}}
			{{- if .Description }}
				{{- /* Split comment string into a slice of one line per element. */ -}}
				{{- $desc := CommentToLines .Description -}}
/**
				{{- range $desc }}
 * {{ . }}
				{{- end }}
 */
			{{- end }}
{{""}}

			{{- /* Write object name. */ -}}
export class {{ .Name | FormatName }} extends BaseClient {
			{{- /* Write methods. */ -}}
			{{- "" }}{{ range $field := .Fields }}
				{{- if Solve . }}
					{{- template "method_solve" $field }}
				{{- else }}
					{{- template "method" $field }}
				{{- end }}
			{{- end }}

{{- if . | IsSelfChainable }}
{{""}}
  /**
   * Call the provided function with current {{ .Name | FormatName }}.
   *
   * This is useful for reusability and readability by not breaking the calling chain.
   */
  with(arg: (param: {{ .Name | FormatName }}) => {{ .Name | FormatName }}) {
    return arg(this)
  }
{{- end }}
}
		{{- end }}
	{{- end }}
{{ end }}
