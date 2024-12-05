{{- /* Write method. */ -}}
{{ define "method" -}}
	{{- $parentName := .ParentObject.Name }}
	{{- $required := GetRequiredArgs .Args }}
	{{- $optionals := GetOptionalArgs .Args }}
	{{- $outputType := .TypeRef | FormatOutputType }}
	{{- if Solve . }}
		{{- $outputType = $parentName | QueryToClient | FormatName }}
	{{- end }}
	{{- if and ($optionals) (eq $parentName "Query") }}
		{{- $parentName = "Client" }}
	{{- end }}
	{{- template "method_comment" . -}}
	{{- template "method_signature" . -}}
{{ "" }}    def {{ .Name | FormatMethod }}
    {{- if gt (len .Args) 0 }}({{ end }}
		{{- $maxReqIndex := Subtract (len $required) 1 }}
		{{- range $index, $value := $required }}
			{{- .Name | FormatArg }}:
			{{- if ne $index $maxReqIndex }}, {{ end }}
		{{- end }}
		{{- if and $required $optionals }}, {{ end }}
		{{- if $optionals }}opts: nil{{ end }}
	{{- if gt (len .Args) 0 }}){{ end }}
	{{- range $index, $value := $required }}
      assert_not_nil(:{{.Name | FormatArg}}, {{.Name | FormatArg}})
	{{- end }}
	{{- if and (eq (len $required) 0) (eq (len $optionals) 0) }}
		{{- if Solve . }}
      n = {{$outputType}}.new(self, @client, '{{.Name}}')
      @client.invoke(n)
		{{- else }}
      {{$outputType}}.new(self, @client, '{{.Name}}')
		{{- end }}
	{{- else }}
		{{- if eq (len $required) 0 }}
      dag_node_args = {}
		{{- else }}
      dag_node_args = {
		{{- range $index, $value := $required }}
        '{{ .Name }}' => {{ .Name | FormatArg }}{{- if ne $index $maxReqIndex }},{{ end }}
		{{- end }}
      }
		{{- end }}
		{{- if $optionals }}
      unless opts.nil?
		{{- range $index, $value := $optionals }}
        dag_node_args['{{ .Name }}'] = opts.{{ .Name | FormatArg }} unless opts.{{ .Name | FormatArg }}.nil?
		{{- end }}
      end
		{{- end }}
		{{- if Solve . }}
      n = {{$outputType}}.new(self, @client, '{{.Name}}', dag_node_args)
      @client.invoke(n)
		{{- else }}
      {{$outputType}}.new(self, @client, '{{.Name}}', dag_node_args)
		{{- end }}
	{{- end }}
    end
{{- end }}
