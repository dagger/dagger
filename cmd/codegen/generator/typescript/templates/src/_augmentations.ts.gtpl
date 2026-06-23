{{- /* Augmentations template.

For each extendable type (Client / Binding / Env) the dep contributes fields
to, emit:

  1. A `declare module "./client.gen.js" { interface X { ... } }` block for
     IDE completion + tsc visibility;
  2. A single exported function `__apply<Dep>Augmentations(scope)` that attaches
     the prototype methods. It is called from the bottom of client.gen.ts (after
     Client/Binding/Env are defined), which avoids the ESM cycle — the dep file
     can't import those classes as values without reintroducing it.

`_ctx` is `protected` on BaseClient. Prototype methods declared outside the
class hierarchy can't access protected members at TS-compile time even though
the access works at runtime, so each body casts `this` to `any` via the
`function (this: any, ...)` signature. */ -}}

{{ define "augmentations" -}}

{{- /* Step 1: type-level merge — one `interface X { ... }` per extendable type
that has any dep-contributed fields. */ -}}
{{- $hasAny := false }}
{{- range .Types }}
  {{- if and (IsExtendableType .) .Fields }}
    {{- $hasAny = true }}
  {{- end }}
{{- end }}
{{- if $hasAny }}

declare module "./client.gen.js" {
{{- range .Types }}
  {{- if and (IsExtendableType .) .Fields }}
  interface {{ .Name | QueryToClient | FormatName }} {
    {{- range $field := .Fields }}
    {{ template "augmentation_signature" $field }}
    {{- end }}
  }
  {{- end }}
{{- end }}
}
{{ "" }}
{{- /* Step 2: runtime prototype assignments, wrapped in a deferred function so
client.gen.ts can call it after defining the extendable type classes. */}}
import type { Client as __Client, Binding as __Binding, Env as __Env } from "./client.gen.js"

export function {{ AugmentFnName .DepName }}(scope: {
  Client: typeof __Client
  Binding: typeof __Binding
  Env: typeof __Env
}) {
  {{- /* Bind the extendable classes as local values so prototype assignment
  and `new Client/Binding/Env(...)` work — they can't be value-imported from
  client.gen.ts (ESM cycle). The matching type aliases let the signatures keep
  referring to them by their bare names. */}}
  const { Client, Binding, Env } = scope
  type Client = __Client
  type Binding = __Binding
  type Env = __Env
{{- range .Types }}
  {{- if and (IsExtendableType .) .Fields }}
    {{- $parent := .Name | QueryToClient | FormatName }}
    {{- range $field := .Fields }}

{{ template "augmentation_method" (Augmentation $parent $field) }}
    {{- end }}
  {{- end }}
{{- end }}
}
{{- else }}

{{- /* No extendable-type fields contributed; emit an empty function so
client.gen.ts can call it unconditionally. */ -}}

export function {{ AugmentFnName .DepName }}() {}
{{- end }}
{{- end }}

{{- /* `augmentation_signature` renders a single field's type signature inside
the `interface X { ... }` block. The dot is an introspection.Field. */ -}}
{{ define "augmentation_signature" -}}
	{{- $required := GetRequiredArgs .Args -}}
	{{- $optionals := GetOptionalArgs .Args -}}
	{{- $parentName := .ParentObject.Name -}}
	{{- if eq $parentName "Query" }}{{ $parentName = "Client" }}{{ end -}}
	{{ .Name | FormatName }}(
		{{- if $required }}{{ template "args" . }}{{ end -}}
		{{- if $optionals -}}
			{{- if $required }}, {{ end }}opts?: {{ $parentName }}{{ .Name | PascalCase }}Opts
		{{- end -}}
	): {{ if Solve . }}Promise<{{ if .TypeRef.IsVoid }}void{{ else }}{{ . | FormatFieldReturnType }}{{ end }}>{{ else }}{{ .TypeRef | FormatOutputType }}{{ end }}
{{- end }}

{{- /* `augmentation_method` renders one prototype-assignment statement.
Input is (Augmentation parent field), exposing .Parent (the TS class name) and
.Field (the introspection.Field). The assignment goes onto
`<Parent>.prototype`, where <Parent> is the local const bound from `scope`
above (the dep file can't value-import Client/Binding/Env from client.gen.ts —
ESM cycle). The body is shared with the class-field methods. */ -}}
{{ define "augmentation_method" -}}
	{{- $field := .Field -}}
	{{- $parent := .Parent -}}
	{{- $required := GetRequiredArgs $field.Args -}}
	{{- $optionals := GetOptionalArgs $field.Args -}}
	{{- $parentName := $field.ParentObject.Name -}}
	{{- if eq $parentName "Query" }}{{ $parentName = "Client" }}{{ end -}}
{{ $parent }}.prototype.{{ $field.Name | FormatName }} = {{ if Solve $field }}async {{ end }}function (this: any
	{{- /* `this: any` is always the first param, so required args and opts each
	always need a leading comma. */ -}}
	{{- if $required -}}, {{ template "args" $field }}{{- end -}}
	{{- if $optionals -}}, opts?: {{ $parentName }}{{ $field.Name | PascalCase }}Opts{{- end -}}
){{ if Solve $field }}: Promise<{{ if $field.TypeRef.IsVoid }}void{{ else }}{{ $field | FormatFieldReturnType }}{{ end }}>{{ else }}: {{ $field.TypeRef | FormatOutputType }}{{ end }} {
	{{- if Solve $field }}
	{{- template "method_solve_body" $field }}
	{{- else }}
	{{- template "method_body" $field }}
	{{- end }}
}
{{- end }}
