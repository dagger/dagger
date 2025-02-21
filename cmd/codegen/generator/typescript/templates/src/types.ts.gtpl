{{- /* Types definition generation.
Export a type for each type or input existing in the GraphQL schema.
 */ -}}
{{ define "types" }}
	{{- range .Types }}
		{{- template "type" . }}
	{{- end }}
{{- end }}

{{ define "type" }}
	{{- /* Generate scalar type. */ -}}
	{{- if IsCustomScalar . }}
		{{- if .Description }}
			{{- /* Split comment string into a slice of one line per element. */ -}}
			{{- $desc := CommentToLines .Description }}
/**
				{{- range $desc }}
 * {{ . }}
				{{- end }}
 */
		{{- end }}
export type {{ .Name }} = string & {__{{ .Name }}: never} {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) {{- end }}
{{ "" }}
	{{- end }}

	{{- /* Generate enum */ -}}
	{{- if IsEnum . }}
		{{- if .Description }}
			{{- /* Split comment string into a slice of one line per element. */ -}}
			{{- $desc := CommentToLines .Description }}
/**
				{{- range $desc }}
 * {{ . }}
				{{- end }}
 */
		{{- end }}
export enum {{ .Name }} { {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) {{- end }}
    {{- $enumName := .Name }}
	{{- range $fields := .EnumValues | SortEnumFields | GroupEnumByValue }}
	{{- $mainFieldName := "" }}
	{{- range $idx, $field := slice $fields }}
		{{- $fieldName := ($field.Name | FormatEnum) }}

		{{- $fieldValue := "" }}
		{{ if eq $idx 0 }}
			{{- $fieldValue = $field.Directives.EnumValue }}
			{{- if not $fieldValue }}
				{{- $fieldValue = $field.Name }}
			{{- end }}
			{{- $fieldValue = $fieldValue | printf "%q" }}

			{{- $mainFieldName = $fieldName }}
		{{ else }}
			{{- $fieldValue = printf "%s.%s" $enumName $mainFieldName }}
		{{ end }}

		{{- if .Description }}
			{{- /* Split comment string into a slice of one line per element. */ -}}
			{{- $desc := CommentToLines .Description }}

  /**
			{{- range $desc }}
   * {{ . }}
			{{- end }}
   */
		{{- end }}
		{{ $fieldName }} = {{ $fieldValue }}, {{- with $field.Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) {{- end }}
	{{- end }}
	{{- end }}
}
	{{- end }}

	{{- /* Generate structure type. */ -}}
	{{- with .Fields }}
		{{- range . }}
			{{- $optionals := GetOptionalArgs .Args }}
			{{- if gt (len $optionals) 0 }}
export type {{ $.Name | QueryToClient }}{{ .Name | PascalCase }}Opts = {
				{{- template "field" $optionals }}
}
{{ "" }}	{{- end }}
		{{- end }}
	{{- end }}

	{{- /* Generate input GraphQL type. */ -}}
	{{- with .InputFields }}
export type {{ $.Name | FormatName }} = {
		{{- template "field" (SortInputFields .) }}
}
{{ "" }}
	{{- end }}

{{- end }}

{{- define "field" }}
	{{- range $i, $field := . }}
		{{- $opt := "" }}

		{{- /* Add ? if field is optional. */ -}}
		{{- if $field.TypeRef.IsOptional }}
			{{- $opt = "?" }}
		{{- end }}

		{{- /* Write description. */ -}}
		{{- if $field.Description }}
			{{- $desc := CommentToLines $field.Description }}

			{{- /* Add extra break line if it's not the first param. */ -}}
			{{- if ne $i 0 }}
{{""}}
			{{- end }}
  /**
			{{- range $desc }}
   * {{ . }}
			{{- end }}
   */
		{{- end }}

		{{- /* Write type, if it's an id it's an output, otherwise it's an input. */ -}}
		{{- if eq $field.Name "id" }}
  {{ $field.Name }}{{ $opt }}: {{ $field.TypeRef | FormatOutputType }} {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) {{- end }}
		{{- else }}
  {{ $field.Name }}{{ $opt }}: {{ $field.TypeRef | FormatInputType }} {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) {{- end }}
		{{- end }}

	{{- end }}
{{- end }}
