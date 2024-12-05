{{- /* Types definition generation.
Export a type for each type or input existing in the GraphQL schema.
 */ -}}
{{ define "types" }}
	{{- template "enums" .}}
	{{- template "customScalars" .}}
{{ "" }}
	{{- template "anticipatedObjects" .}}
	{{- template "anticipatedOpts" .}}
	{{- $types := ValidTypes .Types }}
	{{- range $types }}
		{{- template "type" . }}
	{{- end }}
{{- end }}

{{ define "customScalars" -}}
	{{ $customScalars := CustomScalars .Types }}
	{{- range $i, $type := $customScalars }}
		{{- if ne $i 0 }}
{{ "" }}
		{{- end }}
		{{- template "customScalar" $type }}
	{{- end }}
{{- end }}

{{ define "customScalar" -}}
	{{- if .Description }}
		{{- /* Split comment string into a slice of one line per element. */ -}}
		{{- $desc := CommentToLines .Description }}
		{{- range $desc }}
  #{{ . }}
		{{- end }}
	{{- end }}
  {{ .Name }} = T.type_alias { String }
{{- end }}

{{ define "anticipatedOpts" -}}
	{{ $nodes := NodesWithOpts .Types }}
	{{- range $i, $type := $nodes }}
		{{ "" }}
		{{- template "anticipatedOpt" .}}
	{{- end }}
{{- end }}

{{ define "anticipatedOpt" -}}
	{{- with .Fields }}
		{{- range . }}
			{{- $optionals := GetOptionalArgs .Args }}
  # Optional arguments for {{ .Name | FormatMethod }} on {{ $.Name | QueryToClient | FormatName }}
  class {{ $.Name | QueryToClient }}{{ .Name | PascalCase }}Opts; end
{{ "" }}
		{{- end }}
	{{- end }}
{{- end }}

{{ define "type" }}
	{{- with .Fields }}
		{{- range . }}
			{{- $optionals := GetOptionalArgs .Args }}
			{{- if gt (len $optionals) 0 }}
  # Optional arguments for {{ .Name | FormatMethod }} on {{ $.Name | QueryToClient | FormatName }}
  class {{ $.Name | QueryToClient }}{{ .Name | PascalCase }}Opts
    extend T::Sig
{{ "" }}
			{{- template "opt" $optionals }}
  end
{{ "" }}
			{{- end }}
		{{- end }}
	{{- end }}

	{{- if gt (len .Fields) 0}}
{{ "" }}
	{{- template "object" .}}
{{ "" }}
	{{- end }}

	{{- /* Generate input GraphQL type. */ -}}
	{{- with .InputFields }}
  # Input GraphQL type {{ $.Name | FormatName }}
  class {{ $.Name | FormatName }} < T::Struct
		{{- template "field" (SortInputFields .) }}
  end
{{ "" }}
	{{- end }}
{{- end }}

{{- define "field" }}
	{{- range $i, $field := . }}
		{{- $pre := "" }}
		{{- $post := "" }}
		{{- if $field.TypeRef.IsOptional }}
			{{- $pre = "T.nilable(" }}
			{{- $post = ")" }}
		{{- end }}
		{{- /* Write description. */ -}}
		{{- if $field.Description }}
			{{- $desc := CommentToLines $field.Description }}

			{{- /* Add extra break line if it's not the first param. */ -}}
			{{- if ne $i 0 }}
{{""}}
			{{- end }}
			{{- range $desc }}
    #{{ . }}
			{{- end }}
		{{- end }}
		{{- /* Write type, if it's an id it's an output, otherwise it's an input. */ -}}
		{{- if eq $field.Name "id" }}
    prop :{{ $field.Name | FormatArg }}, {{ $pre }}{{ $field.TypeRef | FormatOutputType }}{{ $post }}
		{{- else }}
    prop :{{ $field.Name | FormatArg }}, {{ $pre }}{{ $field.TypeRef | FormatInputType }}{{ $post }}
		{{- end }}

	{{- end }}
{{- end }}

{{- define "opt" }}
	{{- range $i, $field := . }}
		{{- $pre := "T.nilable(" }}
		{{- $post := ")" }}
		{{- /* Write description. */ -}}
		{{- if $field.Description }}
			{{- $desc := CommentToLines $field.Description }}

			{{- /* Add extra break line if it's not the first param. */ -}}
			{{- if ne $i 0 }}
				{{""}}
			{{- end }}
			{{- range $desc }}
    #{{ . }}
			{{- end }}
		{{- end }}
		{{- if not $field.TypeRef.IsOptional }}
    # Warning: type is set as optional to simplify generated code but this field is required!
		{{- end }}
		{{- $typeName := $field.TypeRef | FormatInputType }}
		{{- /* Write type, if it's an id it's an output, otherwise it's an input. */ -}}
		{{- if eq $field.Name "id" }}
			{{- $typeName = $field.TypeRef | FormatOutputType }}
		{{- end }}
    sig { returns({{ $pre }}{{ $typeName }}{{ $post }}) }
    attr_accessor :{{ $field.Name | FormatArg }}
	{{- end }}
{{- end }}