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
  {{ .Name | FormatMethodName }}({{ template "interface_args" . }}): Promise<{{ . | FormatFieldReturnType }}>
				{{- else }}
  {{ .Name | FormatMethodName }}({{ template "interface_args" . }}): {{ .TypeRef | FormatOutputType }}
				{{- end }}
				{{- template "interface_method_alias" . }}
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
		{{- .Name | FormatArgName }}{{ $opt }}: {{ . | FormatInputType }}
		{{- if or (ne $index $maxIndex) $optionals }}, {{ end }}
	{{- end }}
	{{- if $optionals }}
		{{- "" }}opts?: {{ OptsTypeName $.ParentObject.Name .Name }}
	{{- end }}
{{- end }}

{{- /* Write a deprecated alias for an interface method renamed by identifier
words (see method_alias). The dot is an introspection.Field. */ -}}
{{ define "interface_method_alias" }}
	{{- $name := .Name | FormatMethodName }}
	{{- $legacy := .Name | LegacyMethodName }}
	{{- if ne $name $legacy }}
	{{- $iface := .ParentObject.Name | QueryToClient | FormatName }}
  /**
   * @deprecated use {{ $name }} instead.
   */
  {{ $legacy }}: {{ $iface }}["{{ $name }}"]
	{{- end }}
{{- end }}
