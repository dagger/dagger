{{- /* Write comment's method. */ -}}
{{ define "method_comment" }}
	{{- $required := GetRequiredArgs .Args }}
	{{- $optionals := GetOptionalArgs .Args }}
	{{- $argsDesc := ArgsHaveDescription .Args }}
	{{- $deprecationLines := FormatDeprecation .DeprecationReason }}

	{{- /* Write method description. */ -}}
	{{- if or .Description $argsDesc .IsDeprecated }}
{{""}}
  /**
		{{- /* we split the comment string into a string slice of one line per element */ -}}
		{{- range CommentToLines .Description }}
   * {{ . }}
		{{- end }}
	{{- end }}

	{{- range $required }}
		{{- if .Description }}
		{{- /* Reference current arg to access it in range */ -}}
		{{- $arg := . }}
		{{- /* Write argument description */ -}}
		{{- $desc := CommentToLines .Description  }}
		    {{- range $i, $line := $desc }}
		        {{- /* If it's the first line, add the JSDoc tag, otherwise treat it as a simple line */ -}}
		        {{- if (eq $i 0) }}
   * @param {{ $arg.Name }} {{ $line }}
		        {{- else }}
   * {{ $line }}
		        {{- end }}
		    {{- end }}
		{{- end }}
	{{- end }}

	{{- if ArgsHaveDescription $optionals }}
		{{- range $optionals }}
			{{- if .Description }}
		        {{- /* Reference current arg to access it in range */ -}}
		        {{- $arg := . }}
		        {{- /* Write argument description */ -}}
		        {{- $desc := CommentToLines .Description }}
		        {{- range $i, $line := $desc }}
		            {{- /* If it's the first line, add the JSDoc tag, otherwise treat it as a simple line */ -}}
		            {{- if (eq $i 0) }}
   * @param opts.{{ $arg.Name }} {{ $line }}
		            {{- else }}
   * {{ $line }}
		            {{- end }}
		        {{- end }}
			{{- end }}
		{{- end }}
	{{- end }}

		{{- /* Write deprecation message. */ -}}
		{{- if .IsDeprecated }}
			{{- range $deprecationLines }}
   * {{ . }}
		{{- end }}
	{{- end }}

	{{- if or .Description $argsDesc .IsDeprecated }}
   */
	{{- end }}
{{ "" -}}
{{- end }}
