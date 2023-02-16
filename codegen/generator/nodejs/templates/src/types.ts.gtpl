{{- /* Types definition generation.
Export a type for each type or input existing in the GraphQL schema.
 */ -}}
{{ define "types" }}
	{{- range . }}
		{{- template "type" . }}
	{{- end }}
{{- end }}

{{ define "type" }}
	{{- $typeName := .Name }}
	{{- if eq $typeName "Query" }}
		{{- $typeName = "Client" }}
	{{- end }}

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
export type {{ .Name }} = string & {__{{ .Name }}: never}
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
export enum {{ .Name }} {
		{{- $sortedEnumValues := SortEnumFields .EnumValues }}
		{{- range $sortedEnumValues }}
			{{- if .Description }}
				{{- /* Split comment string into a slice of one line per element. */ -}}
				{{- $desc := CommentToLines .Description }}

  /**
				{{- range $desc }}
   * {{ . }}
				{{- end }}
   */
			{{- end }}
  {{ .Name | FormatEnum }},
		{{- end }}
}
	{{- end }}

	{{- /* Generate structure type. */ -}}
	{{- with .Fields }}
		{{- range . }}
			{{- $optionals := GetOptionalArgs .Args }}
			{{- if gt (len $optionals) 0 }}
export type {{ $typeName }}{{ .Name | PascalCase }}Opts = {
				{{- template "field" $optionals }}
}
{{ "" }}	{{- end }}
		{{- end }}
	{{- end }}

	{{- /* Generate input GraphQL type. */ -}}
	{{- with .InputFields }}
export type {{ $typeName }} = {
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
  {{ $field.Name }}{{ $opt }}: {{ $field.TypeRef | FormatOutputType }}
		{{- else }}
  {{ $field.Name }}{{ $opt }}: {{ $field.TypeRef | FormatInputType }}
		{{- end }}
		
	{{- end }}
{{- end }}
