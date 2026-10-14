{{- define "entrypoint_iface_classes" -}}
{{- $module := . -}}
// Schema names of the module's interfaces, their functions and arguments.
// From engine version v1.0.0 the engine names them with its naming rules
// (getUrl is getURL); __loadSchemaNames asks it for them before anything is
// invoked. Older modules keep the names below.
let __schemaNames = new __SchemaNames()

async function __loadSchemaNames(): Promise<void> {
  const names = await __resolveSchemaNames(
    {{ jsString $module.Name }},
    {{ interfaceNamesLit }},
  )
  if (names) {
    __schemaNames = names
  }
}
{{ range $name := sortedKeysIfaces $module.Interfaces -}}
{{- $iface := index $module.Interfaces $name }}
class __Iface_{{ $iface.Name }} {
  constructor(public _ctx: Context) {}

  static fromID(id: string): __Iface_{{ $iface.Name }} {
    return new __Iface_{{ $iface.Name }}(new Context().selectNode(id, {{ ifaceTypeNameExpr $iface }}))
  }

  async id(): Promise<string> {
    return await this._ctx.select("id").execute()
  }
{{- range $fnName := sortedKeysMethods $iface.Functions }}
{{- $fn := index $iface.Functions $fnName }}

{{ template "entrypoint_iface_method" (dict "Iface" $iface "Fn" $fn "Module" $module) }}
{{- end }}
}

{{ end -}}
{{- end -}}

{{- define "entrypoint_iface_method" -}}
{{- $iface := .Iface -}}
{{- $fn := .Fn -}}
{{- $module := .Module -}}
{{- $sel := ifaceFieldNameExpr $fn.Name -}}
  async {{ $fn.Name }}(
    {{- range $i, $arg := $fn.Arguments -}}
    {{- if $i }}, {{ end -}}
    {{ $arg.Name }}{{ if $arg.IsOptional }}?{{ end }}: any
    {{- end -}}
  ): Promise<any> {
    const __args: Record<string, any> = {}
    {{- range $arg := $fn.Arguments }}
    if ({{ $arg.Name }} !== undefined) __args[{{ ifaceFieldNameExpr $arg.Name }}] = {{ $arg.Name }}
    {{- end }}
    {{- if $fn.ReturnType }}
    {{- $kind := $fn.ReturnType.Kind }}
    {{- if eq $kind "VOID_KIND" }}
    await this._ctx.select({{ $sel }}, __args).execute()
    {{- else if or (eq $kind "OBJECT_KIND") (eq $kind "INTERFACE_KIND") }}
    return new __Iface_{{ $iface.Name }}(this._ctx.select({{ $sel }}, __args))
    {{- else if eq $kind "LIST_KIND" }}
    {{- $inner := $fn.ReturnType.TypeDef }}
    {{- if and $inner (or (eq $inner.Kind "OBJECT_KIND") (eq $inner.Kind "INTERFACE_KIND")) }}
    const __ids = await this._ctx.select({{ $sel }}, __args).select("id").execute<{id: string}[]>()
    {{- if and (eq $inner.Kind "INTERFACE_KIND") (index $module.Interfaces $inner.Name) }}
    return __ids.map(({ id }) => __Iface_{{ $inner.Name }}.fromID(id))
    {{- else if and (eq $inner.Kind "OBJECT_KIND") (index $module.Objects $inner.Name) }}
    return __ids.map(({ id }) => rebuild{{ $inner.Name }}({ id }))
    {{- else }}
    return __ids.map(({ id }) => __loadCoreObject(id, {{ jsString $inner.Name }}))
    {{- end }}
    {{- else }}
    return await this._ctx.select({{ $sel }}, __args).execute()
    {{- end }}
    {{- else }}
    return await this._ctx.select({{ $sel }}, __args).execute()
    {{- end }}
    {{- else }}
    await this._ctx.select({{ $sel }}, __args).execute()
    {{- end }}
  }
{{- end -}}
