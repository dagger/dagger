{{ define "default" }}
const _client = new Client({ ctx: defaultContext })
{{ "" }}
{{- range . }}
    {{- if HasPrefix .Name "__" }}
        {{- /* we ignore types prefixed by __ */ -}}
    {{- else }}
        {{- $name := .Name | QueryToClient }}
        {{- if and .Fields (eq $name "Client") }}
            {{- /* Export Client method publicly for global access */ -}}
            {{- range .Fields }}
export const {{ .Name | FormatName }} = _client.{{ .Name | FormatName }}
            {{- end }}

        {{- end }}
    {{- end }}
{{- end }}

{{- end }}
