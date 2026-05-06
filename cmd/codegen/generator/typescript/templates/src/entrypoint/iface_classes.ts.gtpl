{{- define "entrypoint_iface_classes" -}}
{{- $module := . -}}
{{- range $name := sortedKeysIfaces $module.Interfaces -}}
{{- $iface := index $module.Interfaces $name }}
class __Iface_{{ $iface.Name }} {
  constructor(public _ctx: Context) {}

  static fromID(id: string): __Iface_{{ $iface.Name }} {
    return new __Iface_{{ $iface.Name }}(new Context().select({{ jsString (engineLoadFnName $iface) }}, { id }))
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
  async {{ $fn.Name }}(
    {{- range $i, $arg := $fn.Arguments -}}
    {{- if $i }}, {{ end -}}
    {{ $arg.Name }}{{ if $arg.IsOptional }}?{{ end }}: any
    {{- end -}}
  ): Promise<any> {
    const __args: Record<string, any> = {}
    {{- range $arg := $fn.Arguments }}
    if ({{ $arg.Name }} !== undefined) __args[{{ jsString $arg.Name }}] = {{ $arg.Name }}
    {{- end }}
    {{- if $fn.ReturnType }}
    {{- $kind := $fn.ReturnType.Kind }}
    {{- if eq $kind "VOID_KIND" }}
    await this._ctx.select({{ jsString $fn.Name }}, __args).execute()
    {{- else if or (eq $kind "OBJECT_KIND") (eq $kind "INTERFACE_KIND") }}
    return new __Iface_{{ $iface.Name }}(this._ctx.select({{ jsString $fn.Name }}, __args))
    {{- else if eq $kind "LIST_KIND" }}
    {{- $inner := $fn.ReturnType.TypeDef }}
    {{- if and $inner (or (eq $inner.Kind "OBJECT_KIND") (eq $inner.Kind "INTERFACE_KIND")) }}
    const __ids = await this._ctx.select({{ jsString $fn.Name }}, __args).select("id").execute<{id: string}[]>()
    {{- if and (eq $inner.Kind "INTERFACE_KIND") (index $module.Interfaces $inner.Name) }}
    return __ids.map(({ id }) => __Iface_{{ $inner.Name }}.fromID(id))
    {{- else if and (eq $inner.Kind "OBJECT_KIND") (index $module.Objects $inner.Name) }}
    return __ids.map(({ id }) => rebuild{{ $inner.Name }}({ id }))
    {{- else }}
    return __ids.map(({ id }) => dag.load{{ $inner.Name }}FromID(id as any))
    {{- end }}
    {{- else }}
    return await this._ctx.select({{ jsString $fn.Name }}, __args).execute()
    {{- end }}
    {{- else }}
    return await this._ctx.select({{ jsString $fn.Name }}, __args).execute()
    {{- end }}
    {{- else }}
    await this._ctx.select({{ jsString $fn.Name }}, __args).execute()
    {{- end }}
  }
{{- end -}}
