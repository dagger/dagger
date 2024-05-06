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
export class {{ .Name | QueryToClient | FormatName }} extends BaseClient {
            {{- /* Write private temporary field */ -}}
            {{ range $field := .Fields }}
                {{- if $field.TypeRef.IsScalar }}
  private readonly _{{ $field.Name }}?: {{ $field.TypeRef | FormatOutputType }} = undefined
                {{- end }}
        	{{- end }}

        	{{- /* Create constructor for temporary field */ -}}
{{ "" }}

  /**
   * Constructor is used for internal usage only, do not create object from it.
   */
   constructor(
    parent?: { queryTree?: QueryTree[], ctx: Context },
            {{- range $i, $field := .Fields }}
               {{- if $field.TypeRef.IsScalar }}
     _{{ $field.Name }}?: {{ $field.TypeRef | FormatOutputType }},
               {{- end }}
            {{- end }}
   ) {
     super(parent)
{{ "" }}
            {{- range $i, $field := .Fields }}
               {{- if $field.TypeRef.IsScalar }}
     this._{{ $field.Name }} = _{{ $field.Name }}
               {{- end }}
            {{- end }}
   }

      {{- /* Add custom method to main Client */ -}}
      {{- if .Name | QueryToClient | FormatName | eq "Client" }}

  /**
   * Get the Raw GraphQL client.
   */
  public async getGQLClient() {
    return this._ctx.connection()
  }
      {{- end }}

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
   * Call the provided function with current {{ .Name | QueryToClient }}.
   *
   * This is useful for reusability and readability by not breaking the calling chain.
   */
  with = (arg: (param: {{ .Name | QueryToClient | FormatName }}) => {{ .Name | QueryToClient | FormatName }}) => {
    return arg(this)
  }
{{- end }}
}
		{{- end }}
	{{- end }}
{{ end }}
