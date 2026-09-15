{{- /* Body of a non-solver (synchronous) method.

Reused by:
- method.ts.gtpl         (class-field arrow form: `name = (...) => { body }`)
- _augmentations.ts.gtpl (prototype-assignment form for dep-contributed
  fields on extendable types: `scope.Class.prototype.name = function (this: any, ...) { body }`)

Both contexts give `this` access to the instance (`this._ctx`), so the body
is identical. The dot is an introspection.Field. */ -}}
{{ define "method_body" -}}
	{{- $required := GetRequiredArgs .Args -}}
	{{- $optionals := GetOptionalArgs .Args -}}
	{{- $enums := GetEnumValues .Args }}
	{{- if gt (len $enums) 0 }}
	const metadata = {
	    {{- range $v := $enums }}
	    {{ $v.Name | FormatName -}}: { is_enum: true, value_to_name: {{ $v | GetInputEnumValueType }}ValueToName },
	    {{- end }}
	}
{{ "" -}}
	{{- end }}

    const ctx = this._ctx.select(
      "{{ .Name }}",
{{- if or $required $optionals }}
      { {{""}}
      		{{- with $required }}
				{{- template "call_args" $required }}
			{{- end }}

      		{{- with $optionals }}
      			{{- if $required }}, {{ end -}}
      ...opts
			{{- end -}}
			{{- if gt (len $enums) 0 -}}, __metadata: metadata{{- end -}}
{{""}} },{{- end }}
    )

	{{- if .TypeRef }}
		{{- if .TypeRef.IsInterface }}
    return new _{{ .TypeRef | FormatOutputType }}Client(ctx)
		{{- else }}
    return new {{ .TypeRef | FormatOutputType }}(ctx)
		{{- end }}
	{{- end }}
{{- end }}
