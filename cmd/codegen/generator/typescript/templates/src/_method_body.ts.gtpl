{{- /* Body of a non-solver method.
Re-used by:
- method.ts.gtpl       (class-field arrow form: `name = (...) => { body }`)
- _augmentations.ts.gtpl (prototype assignment form for dep-augmented
  extendable types: `Class.prototype.name = function (this: Class, ...) { body }`)
Both contexts give `this` access to the instance, so the body itself is
identical. */ -}}
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
    return new {{ .TypeRef | FormatOutputType }}(ctx)
	{{- end }}
{{- end }}
