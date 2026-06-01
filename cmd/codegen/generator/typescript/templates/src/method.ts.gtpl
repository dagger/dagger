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
		{{- "" }}opts?: {{ $parentName }}{{ .Name | PascalCase }}Opts {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }})
		{{ "" }}
		{{- end }}
	{{- end }}

	{{- /* Write return type. */ -}}
	{{- "" }}){{- "" }}: {{ .TypeRef | FormatOutputType }} => { {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) {{- end }}
	{{- template "method_body" . }}
  }
{{- end }}
