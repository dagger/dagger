{{- /* Write comments. */ -}}
{{ define "method_comment" -}}
	{{- $required := GetRequiredArgs .Args }}
	{{- $optionals := GetOptionalArgs .Args }}
	{{- if or .Description (ArgsHaveDescription .Args) .IsDeprecated }}
		{{- if .Description }}
			{{- $desc := CommentToLines .Description }}
			{{- range $desc }}
    #{{ . }}
			{{- end }}
		{{- end }}
		{{- if ArgsHaveDescription .Args }}
    #
			{{- range $required }}
				{{- if .Description }}
					{{- $desc := CommentToLines .Description }}
					{{- range $desc }}
    # @param {{ $desc }}
					{{- end }}
				{{- end }}
			{{- end }}
			{{- range $optionals }}
				{{- if .Description }}
					{{- $desc := CommentToLines .Description }}
					{{- range $desc }}
    # @param {{ $desc }}
					{{- end }}
				{{- end }}
			{{- end }}
		{{- end }}
		{{- if .IsDeprecated }}
			{{- $desc := FormatDeprecation .DeprecationReason }}
			{{- range $desc }}
    #{{ . }}
			{{- end }}
		{{- end }}
	{{- end }}
{{- end }}
