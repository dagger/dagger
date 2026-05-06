{{- define "entrypoint_imports" -}}
{{- range plannedImports -}}
{{- if and .SideEffect (not .Names) (not .Namespace) }}
import "{{ .From }}"
{{- else -}}
{{- if .SideEffect }}
import "{{ .From }}"
{{- end -}}
{{- if .Namespace }}
import {{ .Namespace }} from "{{ .From }}"
{{- end -}}
{{- if .Names }}
import { {{ range $i, $n := .Names }}{{ if $i }}, {{ end }}{{ $n }}{{ end }} } from "{{ .From }}"
{{- end -}}
{{- end -}}
{{- end }}
{{- end -}}
