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
			{{- $name := .Name | FormatName }}
			{{- if eq $name "Query" }}
export default class Client extends BaseClient {
			{{- else }}
export class {{ .Name | FormatName }} extends BaseClient {
{{""}}
			{{- end }}

			{{- /* Write methods. */ -}}
			{{- "" }}{{ range $field := .Fields }}
				{{- if Solve . }}
					{{- template "method_solve" $field }}
				{{- else }}
					{{- template "method" $field }}
				{{- end }}
			{{- end }}

{{- if ne $name "Query" }}
{{""}}
  /**
   * Chain objects together
   * @example
   * ```ts
   *	function AddAFewMounts(c) {
   *			return c
   *			.withMountedDirectory("/foo", new Client().host().directory("/Users/slumbering/forks/dagger"))
   *			.withMountedDirectory("/bar", new Client().host().directory("/Users/slumbering/forks/dagger/sdk/nodejs"))
   *	}
   *
   * connect(async (client) => {
   *		const tree = await client
   *			.container()
   *			.from("alpine")
   *			.withWorkdir("/foo")
   *			.with(AddAFewMounts)
   *			.withExec(["ls", "-lh"])
   *			.stdout()
   * })
   *```
   */
  with(arg: (param: {{ .Name | FormatName }}) => {{ .Name | FormatName }}) {
    return arg(this)
  }
{{- end }}
}
		{{- end }}
	{{- end }}
{{ end }}
