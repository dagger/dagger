{{- define "entrypoint_register" -}}
{{- $module := . -}}
async function register(): Promise<string> {
  let mod = dag.module_()
{{- if $module.Description }}
  mod = mod.withDescription({{ jsString $module.Description }})
{{- end }}
{{- range $name := sortedKeysObjects $module.Objects }}
{{- $obj := index $module.Objects $name }}
  let obj_{{ $obj.Name }} = {{ renderObjectDef $obj }}
{{- range $mName := sortedKeysMethods $obj.Methods }}
{{- $fn := index $obj.Methods $mName }}
  obj_{{ $obj.Name }} = obj_{{ $obj.Name }}.withFunction({{ renderFunctionExpr $fn }})
{{- end }}
{{- range $pName := sortedKeysProps $obj.Properties }}
{{- $prop := index $obj.Properties $pName }}
{{- if $prop.IsExposed }}
  obj_{{ $obj.Name }} = obj_{{ $obj.Name }}{{ renderFieldCall $prop }}
{{- end }}
{{- end }}
{{- if $obj.Constructor }}
  obj_{{ $obj.Name }} = obj_{{ $obj.Name }}.withConstructor(dag.function_("", obj_{{ $obj.Name }})
    {{- range $arg := $obj.Constructor.Arguments }}{{ renderArgCall $arg }}{{ end }})
{{- end }}
  mod = mod.withObject(obj_{{ $obj.Name }})
{{- end }}
{{- range $name := sortedKeysEnums $module.Enums }}
{{- $e := index $module.Enums $name }}
  let enum_{{ $e.Name }} = {{ renderEnumDef $e }}
{{- range $vName := sortedKeysEnumValues $e.Values }}
{{- $v := index $e.Values $vName }}
  enum_{{ $e.Name }} = enum_{{ $e.Name }}{{ renderEnumMemberCall $v }}
{{- end }}
  mod = mod.withEnum(enum_{{ $e.Name }})
{{- end }}
{{- range $name := sortedKeysIfaces $module.Interfaces }}
{{- $iface := index $module.Interfaces $name }}
  let iface_{{ $iface.Name }} = {{ renderInterfaceDef $iface }}
{{- range $fnName := sortedKeysMethods $iface.Functions }}
{{- $fn := index $iface.Functions $fnName }}
  iface_{{ $iface.Name }} = iface_{{ $iface.Name }}.withFunction({{ renderFunctionExpr $fn }})
{{- end }}
  mod = mod.withInterface(iface_{{ $iface.Name }})
{{- end }}
  return await mod.id()
}
{{- end -}}
