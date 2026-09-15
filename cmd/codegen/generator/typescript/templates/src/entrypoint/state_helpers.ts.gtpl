{{- define "entrypoint_state_helpers" -}}
{{- $module := . -}}
{{- range $name := sortedKeysObjects $module.Objects -}}
{{- $obj := index $module.Objects $name }}
{{- if and (eq $obj.Kind "class") (not $obj.IsExported) }}
const __cls_{{ $obj.Name }}: any = (() => { const c = getRegisteredClass({{ jsString $obj.Name }}); if (!c) throw new Error("class {{ $obj.Name }} is not registered (missing @object() decorator?)"); return c })()

{{ end -}}
{{ template "entrypoint_class_rebuild" (dict "Obj" $obj "Module" $module) }}

{{ template "entrypoint_class_serialize" (dict "Obj" $obj "Module" $module) }}

{{ end -}}
{{- end -}}

{{- define "entrypoint_class_rebuild" -}}
{{- $obj := .Obj -}}
function rebuild{{ $obj.Name }}(state: any): {{ classTypeRef $obj }} {
{{- if eq $obj.Kind "class" }}
  const __obj = Object.assign(Object.create({{ classRuntimeRef $obj }}.prototype), state ?? {})
{{- else }}
  const __obj: any = { ...(state ?? {}) }
{{- end }}
  if (state) {
{{- range $name := sortedKeysProps $obj.Properties }}
{{- $prop := index $obj.Properties $name }}
{{- if $prop.Type }}
{{- $field := propFieldName $prop }}
{{- $xform := needsTransform $prop.Type }}
{{- $renamed := ne $field $prop.Name }}
{{- if or $xform $renamed }}
    if (state[{{ jsString $field }}] !== undefined && state[{{ jsString $field }}] !== null) {
      __obj[{{ jsString $prop.Name }}] = {{ if $xform }}{{ coerceExpr (printf "state[%s]" (jsString $field)) $prop.Type }}{{ else }}state[{{ jsString $field }}]{{ end }}
    }
{{- if $renamed }}
    delete __obj[{{ jsString $field }}]
{{- end }}
{{- end }}
{{- end }}
{{- end }}
  }
  return __obj
}
{{- end -}}

{{- define "entrypoint_class_serialize" -}}
{{- $obj := .Obj -}}
async function serialize{{ $obj.Name }}(__obj: {{ classTypeRef $obj }}): Promise<any> {
  if (__obj === null || __obj === undefined) return __obj
  const __state: any = { ...__obj }
{{- range $name := sortedKeysProps $obj.Properties }}
{{- $prop := index $obj.Properties $name }}
{{- if $prop.Type }}
{{- $field := propFieldName $prop }}
{{- $xform := needsTransform $prop.Type }}
{{- $renamed := ne $field $prop.Name }}
{{- if or $xform $renamed }}
  if ((__obj as any)[{{ jsString $prop.Name }}] !== undefined && (__obj as any)[{{ jsString $prop.Name }}] !== null) {
    __state[{{ jsString $field }}] = {{ if $xform }}{{ serializeExpr (printf "(__obj as any)[%s]" (jsString $prop.Name)) $prop.Type }}{{ else }}(__obj as any)[{{ jsString $prop.Name }}]{{ end }}
  }
{{- if $renamed }}
  delete __state[{{ jsString $prop.Name }}]
{{- end }}
{{- end }}
{{- end }}
{{- end }}
  return __state
}
{{- end -}}
