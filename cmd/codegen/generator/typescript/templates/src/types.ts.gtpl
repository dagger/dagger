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
		{{- $needsUnscopedEnums := CheckVersionCompatibility "v0.15.0" | not }}
		{{- $enumName := .Name }}
		{{- if .Description }}
			{{- /* Split comment string into a slice of one line per element. */ -}}
			{{- $desc := CommentToLines .Description }}
/**
				{{- range $desc }}
 * {{ . }}
				{{- end }}
 */
		{{- end }}
export enum {{ $enumName }} { {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) {{- end }}
		{{- range $fields := .EnumValues | SortEnumFields | GroupEnumByValue }}
			{{- $mainFieldName := "" }}
			{{- range $idx, $field := slice $fields }}
				{{- $fieldName := ($field.Name | FormatEnum) }}
				{{- $fullFieldName := printf "%s.%s" $enumName ($field.Name | FormatEnum) }}

				{{- /* Get the enum value from the directive or the field name. */ -}}
				{{- $fieldValue := "" }}
				{{- if eq $idx 0 }}
					{{- $fieldValue = $field.Directives.EnumValue }}
					{{- if not $fieldValue }}
						{{- $fieldValue = $field.Name }}
					{{- end }}
					{{- $fieldValue = $fieldValue | printf "%q" }}
					{{- $mainFieldName = $fullFieldName }}
				{{- else }}
					{{- $fieldValue = $mainFieldName }}
				{{- end }}

			  {{- if or .Description .IsDeprecated }}
				  {{- /* Split comment string into a slice of one line per element. */ -}}
				  {{- $desc := CommentToLines .Description }}

  /**
				  {{- range $desc }}
   * {{ . }}
				  {{- end }}
				  {{- if and $desc .IsDeprecated }}
   *
				  {{- end }}
				  {{- if .IsDeprecated }}
					  {{- $deprecationLines := FormatDeprecation .DeprecationReason }}
					  {{- range $deprecationLines }}
   * {{ . }}
					  {{- end }}
				  {{- end }}
   */

			  {{- end }}
  {{ $fieldName }} = {{ $fieldValue }}, {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) {{- end }}
			{{- end }}
	{{- end }}
}

/**
 * Utility function to convert a {{ .Name }} value to its name so
 * it can be uses as argument to call a exposed function.
 */
function {{ .Name | PascalCase }}ValueToName(value: {{ .Name }}): string {
  switch (value) {
	{{- range $fields := .EnumValues | SortEnumFields | GroupEnumByValue -}}
		{{- $field := index $fields 0 }}
		{{- $fieldName := ($field.Name | FormatEnum) }}
    case {{ $enumName }}.{{ $fieldName }}:
      return "{{ $field.Name }}"
	{{- end }}
    default:
      return value
  }
}

/**
 * Utility function to convert a {{ $enumName }} name to its value so
 * it can be properly used inside the module runtime.
 */
function {{ $enumName | PascalCase }}NameToValue(name: string): {{ $enumName }} {
  switch (name) {
	{{- range $fields := .EnumValues | SortEnumFields | GroupEnumByValue -}}
		{{- $field := index $fields 0 }}
		{{- $fieldName := ($field.Name | FormatEnum) }}
    case "{{ $field.Name }}":
      return {{ $enumName }}.{{ $fieldName }}
	{{- end }}
    default:
      return name as {{ $enumName }}
  }
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

		{{- /* Write description and deprecated annotation. */ -}}
		{{- if or $field.Description $field.IsDeprecated }}
			{{- if ne $i 0 }}
{{""}}
			{{- end }}
  /**
			{{- if $field.Description }}
				{{- range CommentToLines $field.Description }}
   * {{ . }}
				{{- end }}
			{{- end }}
			{{- if and $field.Description $field.IsDeprecated }}
   *
			{{- end }}
			{{- if $field.IsDeprecated }}
				{{- $deprecationLines := FormatDeprecation $field.DeprecationReason }}
				{{- range $deprecationLines }}
   * {{ . }}
				{{- end }}
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
