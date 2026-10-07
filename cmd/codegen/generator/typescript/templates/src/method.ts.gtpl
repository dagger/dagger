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
	{{- "" }}  {{ .Name | FormatMethodName }} = (

	{{- /* Write required arguments. */ -}}
	{{- if $required }}
		{{- template "args" . }}
	{{- end }}

	{{- /* Write optional arguments */ -}}
	{{- if $optionals }}
		{{- /* Insert a comma if there was previous required arguments. */ -}}
		{{- if $required }}, {{ end }}
		{{- "" }}opts?: {{ OptsTypeName $parentName .Name }} {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) 
		{{ "" }} 
		{{- end }}
	{{- end }}

	{{- /* Write return type. */ -}}
	{{- "" }}){{- "" }}: {{ .TypeRef | FormatOutputType }} => { {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) {{- end }}
	{{- /* Body is shared with the dep prototype augmentations. */ -}}
	{{- template "method_body" . }}
  }
	{{- template "method_alias" . }}
{{- end }}

{{- /* Write a deprecated alias under the name a method had before identifier
words, when it differs. The dot is an introspection.Field. */ -}}
{{ define "method_alias" }}
	{{- $name := .Name | FormatMethodName }}
	{{- $legacy := .Name | LegacyMethodName }}
	{{- if ne $name $legacy }}
	{{- $class := .ParentObject.Name | QueryToClient | FormatName }}

  /**
   * @deprecated use {{ $name }} instead.
   */
  {{ $legacy }}: {{ $class }}["{{ $name }}"] = (...args) =>
    this.{{ $name }}(...args)
	{{- end }}
{{- end }}
