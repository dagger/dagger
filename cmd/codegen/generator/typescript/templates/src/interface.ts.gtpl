{{- /* Generate TypeScript interface from GraphQL interface type. */ -}}
{{ define "interface" }}
	{{- with . }}
		{{- if .Fields }}

			{{- /* Write description. */ -}}
			{{- if .Description }}
				{{- /* Split comment string into a slice of one line per element. */ -}}
				{{- $desc := CommentToLines .Description -}}
/**
				{{- range $desc }}
 * {{ . }}
				{{- end }}
 */
			{{- end }}
{{""}}

			{{- /* Write interface definition (TypeScript structural typing). */ -}}
export interface {{ .Name | FormatName }} { {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) {{- end }}
			{{- range $field := .Fields }}
				{{- if Solve . }}
  {{ .Name | FormatName }}({{ template "interface_args" . }}): Promise<{{ . | FormatFieldReturnType }}>
				{{- else }}
  {{ .Name | FormatName }}({{ template "interface_args" . }}): {{ .TypeRef | FormatOutputType }}
				{{- end }}
			{{- end }}
}

{{""}}
			{{- /* Write concrete client class for query builder instantiation. */ -}}
export class _{{ .Name | FormatName }}Client extends BaseClient { {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) {{- end }}
            {{- /* Write private temporary field */ -}}
            {{ range $field := .Fields }}
                {{- if $field.TypeRef.IsScalar }}
  private readonly _{{ $field.Name }}?: {{ $field | FormatFieldOutputType }} = undefined
                {{- end }}
        	{{- end }}

        	{{- /* Create constructor for temporary field */ -}}
{{ "" }}

  /**
   * Constructor is used for internal usage only, do not create object from it.
   */
   constructor(
    ctx?: Context,
            {{- range $i, $field := .Fields }}
               {{- if $field.TypeRef.IsScalar }}
     _{{ $field.Name }}?: {{ $field | FormatFieldOutputType }},
               {{- end }}
            {{- end }}
   ) {
     super(ctx)
{{ "" }}
            {{- range $i, $field := .Fields }}
               {{- if $field.TypeRef.IsScalar }}
     this._{{ $field.Name }} = _{{ $field.Name }}
               {{- end }}
            {{- end }}
   }

			{{- /* Write methods. */ -}}
			{{- "" }}{{ range $field := .Fields }}
				{{- if Solve . }}
					{{- template "method_solve" $field }}
				{{- else }}
					{{- template "method" $field }}
				{{- end }}
			{{- end }}
}
		{{- end }}
	{{- end }}
{{ end }}

{{- /* Write interface method arguments (just signatures, no body). */ -}}
{{ define "interface_args" }}
	{{- $required := GetRequiredArgs .Args }}
	{{- $optionals := GetOptionalArgs .Args }}
	{{- $maxIndex := Subtract (len $required) 1 }}

	{{- range $index, $value := $required }}
		{{- $opt := "" }}
		{{- if .TypeRef.IsOptional }}
			{{- $opt = "?" }}
		{{- end }}
		{{- .Name | FormatName }}{{ $opt }}: {{ . | FormatInputValueType }}
		{{- if or (ne $index $maxIndex) $optionals }}, {{ end }}
	{{- end }}
	{{- if $optionals }}
		{{- "" }}opts?: {{ $.ParentObject.Name }}{{ .Name | PascalCase }}Opts
	{{- end }}
{{- end }}
