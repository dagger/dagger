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
	{{- "" }}  async {{ .Name | FormatName }}(

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

    {{- /* If it's a scalar, make possible to return its already filled value */ -}}
    {{- if and (.TypeRef.IsScalar) (ne .ParentObject.Name "Query") (not $convertID) }}
    if (this._{{ .Name }}) {
      return this._{{ .Name }}
    }
{{ "" }}
    {{- end }}

    {{- /* Store promise return type that might be update in case of array */ -}}
    {{- $promiseRetType := . | FormatReturnType -}}

    {{- if and .TypeRef.IsList (IsListOfObject .TypeRef) }}
    type {{ .Name | ToLowerCase }} = {
            {{- range $v := . | GetArrayField }}
      {{ $v.Name | ToLowerCase }}: {{ $v.TypeRef | FormatOutputType }}
            {{- end }}
    }
{{ "" }}
    {{- $promiseRetType = printf "%s[]" (.Name | ToLowerCase) }}
    {{- end }}

	{{- $enums := GetEnumValues .Args }}
	{{- if gt (len $enums) 0 }}
	const metadata: Metadata = {
	    {{- range $v := $enums }}
	    {{ $v.Name | FormatName -}}: { is_enum: true },
	    {{- end }}
	}
{{ "" -}}

	{{- end }}

	{{- if .TypeRef }}
    {{ if not $convertID }}const response: Awaited<{{ $promiseRetType }}> = {{ end }}await computeQuery(
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
				{{- "" }}...opts{{- if gt (len $enums) 0 -}}, __metadata: metadata{{- end -}}
			{{- end }}
{{- "" }} },
		{{- end }}
        },
        {{- /* Add subfields */ -}}
        {{- if and .TypeRef.IsList (IsListOfObject .TypeRef) }}
        {
          operation: "{{- range $i, $v := . | GetArrayField }}{{if $i }} {{ end }}{{ $v.Name | ToLowerCase }}{{- end }}"
        },
        {{- end }}
      ],
      this.client
    )

    {{ if $convertID -}}
    return this
    {{- else -}}
        {{- if and .TypeRef.IsList (IsListOfObject .TypeRef) }}
    return response.map(
      (r) => new {{ . | FormatReturnType | ToSingleType }}(
      {
        queryTree: this.queryTree,
        host: this.clientHost,
        sessionToken: this.sessionToken,
      },
        {{- range $v := . | GetArrayField }}
        r.{{ $v.Name | ToLowerCase }},
        {{- end }}
      )
    )
        {{- else }}
    return response
        {{- end }}
    {{- end }}
  }
	{{- end }}
{{- end }}
