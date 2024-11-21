{{- /* Generate class from GraphQL struct query type. */ -}}
{{ define "object" -}}
	{{- with . -}}
	{{- if .Fields }}
			{{- /* Write description. */ -}}
			{{- if .Description }}
				{{- /* Split comment string into a slice of one line per element. */ -}}
				{{- $desc := CommentToLines .Description -}}
				{{- range $desc -}}
{{ "  " }}#{{ . }}
{{ "" }}
				{{- end }}
			{{- else }}
{{ "  " }}# {{ .Name | QueryToClient | FormatName }} class
{{ "" }}
			{{- end }}
			{{- /* Write object name. */ -}}
{{ "  " }}class {{ .Name | QueryToClient | FormatName }} < Node
      {{- $first := true -}}
      {{- /* Add custom method to main Client */ -}}
      {{- if .Name | QueryToClient | FormatName | eq "Client" }}
      {{- $first = false -}}
{{ "" }}
    attr_reader :client

      {{- end -}}
			{{- /* Write methods. */ -}}
			{{- range $field := .Fields }}
				{{- if not $first }}
{{ "" }}
				{{- end }}
				{{- $first = false -}}
				{{- if eq $field.Name "id" }}
    # Return the Node ID for the GraphQL entity
    # @return [String]
    def id
      @client.invoke(Node.new(self, @client, 'id'))
    end
				{{- else }}
					{{- template "method" $field }}
				{{- end }}
			{{- end }}
			{{- if . | IsSelfChainable }}
{{ "" }}
    def with(fun)
      fun.call(self)
    end
			{{- end }}
  end
		{{- end -}}
	{{- end -}}
{{- end -}}
