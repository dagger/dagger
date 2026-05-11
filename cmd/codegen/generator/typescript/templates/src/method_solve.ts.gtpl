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

    {{- /* If it's a scalar, make possible to return its already filled value */ -}}
    {{- if and (.TypeRef.IsScalar) (ne .ParentObject.Name "Query") (not $convertID) }}
    if (this._{{ .Name }}) {
        {{- if .TypeRef.IsVoid }}
      return
        {{- else }}
      return this._{{ .Name }}
        {{- end }}
    }
{{ "" }}
    {{- end }}

    {{- /* Store promise return type that might be update in case of array */ -}}
    {{- $promiseRetType := . | FormatFieldReturnType -}}

    {{- if and .TypeRef.IsList (IsListOfObject .TypeRef) }}
    type {{ .Name | ToLowerCase }} = {
            {{- range $v := . | GetArrayField }}
      {{ $v.Name | ToLowerCase }}: {{ $v | FormatFieldOutputType }}
            {{- end }}
    }
{{ "" }}
    {{- $promiseRetType = printf "%s[]" (.Name | ToLowerCase) }}
    {{- end }}

	{{- $enums := GetEnumValues .Args }}
	{{- if gt (len $enums) 0 }}
	const metadata = {
	    {{- range $v := $enums }}
	    {{ $v.Name | FormatName -}}: { is_enum: true, value_to_name: {{ $v | GetInputEnumValueType }}ValueToName },
	    {{- end }}
	}
{{ "" -}}

	{{- end }}

	{{- if .TypeRef }}
    const ctx = this._ctx.select(
      "{{ .Name }}",
      {{- /* Insert arguments. */ -}}
		{{- if or $required $optionals }}
      { {{""}}
      		{{- with $required }}
				{{- template "call_args" $required }}
			{{- end }}

      		{{- with $optionals }}
      			{{- if $required }}, {{ end }}
				{{- "" }}...opts
			{{- end }}
      {{- if gt (len $enums) 0 -}}, __metadata: metadata{{- end -}}
{{- "" }}},
		{{- end }}
    ){{- /* Add subfields */ -}}
      {{- if and .TypeRef.IsList (IsListOfObject .TypeRef) }}.select("{{- range $i, $v := . | GetArrayField }}{{if $i }} {{ end }}{{ $v.Name | ToLowerCase }}{{- end }}")
      {{- end }}

    {{ if not .TypeRef.IsVoid }}const response: Awaited<{{ if $convertID }}{{ . | FormatFieldOutputType }}{{ else }}{{ $promiseRetType }}{{ end }}> = {{ end }}await ctx.execute()

    {{ if $convertID -}}
      {{- if IsInterface .ParentObject }}
    return new _{{ $promiseRetType | FormatProtected | FormatName }}Client(ctx.copy().selectNode(response, "{{ $promiseRetType | FormatProtected }}"))
      {{- else }}
    return new {{ $promiseRetType | FormatProtected | FormatName }}(ctx.copy().selectNode(response, "{{ $promiseRetType | FormatProtected }}"))
      {{- end }}
    {{- else if not .TypeRef.IsVoid -}}
        {{- if and .TypeRef.IsList (IsListOfObject .TypeRef) }}
    return response.map((r) => new {{ . | FormatReturnType | ToSingleType | FormatProtected | FormatName }}(ctx.copy().selectNode(r.id, "{{ . | FormatReturnType | ToSingleType | FormatProtected }}")))
        {{- else if and .TypeRef.IsList (IsListOfEnum .TypeRef) -}}
    return response.map((r) => {{ . | FormatReturnType | ToSingleType }}NameToValue(r))
        {{- else if .TypeRef.IsEnum }}
        {{- /* If it's an Enum, we receive the member name so we must convert it to the actual value */ -}}
    return {{ $promiseRetType }}NameToValue(response)
        {{- else }}
    return response
        {{- end }}
    {{- end }}
  }
	{{- end }}
{{- end }}
