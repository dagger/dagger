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
	{{- "" }}  {{ .Name | FormatName }} = async (

	{{- /* Write required arguments. */ -}}
	{{- if $required }}
		{{- template "args" . }}
	{{- end }}

	{{- /* Write optional arguments. */ -}}
	{{- if $optionals }}
		{{- /* Insert a comma if there was previous required arguments. */ -}}
		{{- if $required }}, {{ end }}
    opts?: {{ $parentName }}{{ .Name | PascalCase }}Opts {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) 
    {{ "" }} 
    {{- end }}
	{{- end }}

	{{- /* Write return type */ -}}
	{{- "" }}): Promise<{{ if .TypeRef.IsVoid }}void{{ else }}{{ . | FormatFieldReturnType }}{{ end }}> => { {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) {{- end }}
	{{- /* Body is shared with the dep prototype augmentations. */ -}}
	{{- template "method_solve_body" . }}
  }
{{- end }}
