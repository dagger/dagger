{{- /* Write solver method that returns a Promise.  */ -}}
{{ define "method_solve" }}
	{{- $parentName := .ParentObject.Name }}
	{{- $required := GetRequiredArgs .Args }}
	{{- $optionals := GetOptionalArgs .Args }}
    {{- $convertID := ConvertID . }}

	{{- if and ($optionals) (eq $parentName "Query") }}
		{{- $parentName = "Client" }}
	{{- end }}

	{{- /* Write method comment. */ -}}
	{{- template "method_comment" . }}
	{{- /* Write async method name. */ -}}
	{{- "" }}  async {{ .Name }}(

	{{- /* Write required arguments. */ -}}
	{{- if $required }}
		{{- template "args" . }}
	{{- end }}

	{{- /* Write optional arguments. */ -}}
	{{- if $optionals }}
		{{- /* Insert a comma if there was previous required arguments. */ -}}
		{{- if $required }}, {{ end }}
		{{- "" }}opts?: {{ $parentName | PascalCase }}{{ .Name | PascalCase }}Opts
	{{- end }}

	{{- /* Write return type */ -}}
	{{- "" }}): Promise<{{ . | FormatReturnType }}> {

	{{- if .TypeRef }}
    {{ if not $convertID }}const response: Awaited<{{ . | FormatReturnType }}> = {{ end }}await computeQuery(
      [
        ...this._queryTree,
        {
          operation: "{{ .Name }}",

		{{- /* Insert arguments. */ -}}
		{{- if or $required $optionals }}
          args: { {{""}}
      		{{- with $required }}
				{{- template "call_args" $required }}
			{{- end }}

      		{{- with $optionals }}
      			{{- if $required }}, {{ end }}
				{{- "" }}...opts
			{{- end }}
{{- "" }} },
		{{- end }}
        },
      ],
      this.client
    )

    {{ if $convertID -}}
    return this
    {{- else -}}
    return response
    {{- end }}
  }
	{{- end }}
{{- end }}
