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
	{{- "" }}  {{ .Name | FormatName }} = (

	{{- /* Write required arguments. */ -}}
	{{- if $required }}
		{{- template "args" . }}
	{{- end }}

	{{- /* Write optional arguments */ -}}
	{{- if $optionals }}
		{{- /* Insert a comma if there was previous required arguments. */ -}}
		{{- if $required }}, {{ end }}
		{{- "" }}opts?: {{ $parentName | PascalCase }}{{ .Name | PascalCase }}Opts {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) 
		{{ "" }} 
		{{- end }}
	{{- end }}

	{{- /* Write return type. */ -}}
	{{- "" }}){{- "" }}: {{ .TypeRef | FormatOutputType }} => { {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) {{- end }}

	{{- $enums := GetEnumValues .Args }}
	{{- if gt (len $enums) 0 }}
	const metadata = {
	    {{- range $v := $enums }}
	    {{ $v.Name | FormatName -}}: { is_enum: true },
	    {{- end }}
	}
{{ "" -}}
	{{- end }}

    const ctx = this._ctx.select(
      "{{ .Name }}",
{{- if or $required $optionals }}
      { {{""}}
      		{{- with $required }}
				{{- template "call_args" $required }}
			{{- end }}

      		{{- with $optionals }}
      			{{- if $required }}, {{ end -}}
      ...opts
			{{- end -}}
			{{- if gt (len $enums) 0 -}}, __metadata: metadata{{- end -}}
{{""}} },{{- end }}
    )

	{{- if .TypeRef }}
    return new {{ .TypeRef | FormatOutputType }}(ctx)
	{{- end }}
  }
{{- end }}
