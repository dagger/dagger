{{- /* Write method. */ -}}
{{ define "method" }}
	{{- $parentName := .ParentObject.Name }}
	{{- $required := GetRequiredArgs .Args }}
	{{- $optionals := GetOptionalArgs .Args }}

	{{- if and ($optionals) (eq $parentName "Query") }}
		{{- $parentName = "Client" }}
	{{- end }}

	{{- /* Write method comment. */ -}}
	{{- template "method_comment" . }}

	{{- /* Write method name. */ -}}
	{{- "" }}  {{ .Name -}}(

	{{- /* Write required arguments. */ -}}
	{{- if $required }}
		{{- template "args" . }}
	{{- end }}

	{{- /* Write optional arguments */ -}}
	{{- if $optionals }}
		{{- /* Insert a comma if there was previous required arguments. */ -}}
		{{- if $required }}, {{ end }}
		{{- "" }}opts?: {{ $parentName | PascalCase }}{{ .Name | PascalCase }}Opts
	{{- end }}

	{{- /* Write return type. */ -}}
	{{- "" }}){{- "" }}: {{ .TypeRef | FormatOutputType  }} {
	
	{{- if .TypeRef }}
    return new {{ .TypeRef | FormatOutputType }}({
      queryTree: [
        ...this._queryTree,
        {
          operation: "{{ .Name}}",

		{{- /* Insert arguments. */ -}}
		{{- if or $required $optionals }}
          args: { {{""}}
      		{{- with $required }}
				{{- template "call_args" $required }}
			{{- end }}

      		{{- with $optionals }}
      			{{- if $required }}, {{ end -}}
        ...opts
			{{- end -}}
{{""}} },
		{{- end }}
        },
      ],
      host: this.clientHost,
      sessionToken: this.sessionToken,
    })
	{{- end }}
  }
{{- end }}
