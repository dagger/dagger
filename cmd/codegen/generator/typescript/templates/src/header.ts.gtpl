{{- /* Header template.
A static file to define BaseClient class that will be
inherited by futures objects and common types.
 */ -}}
{{ define "header" -}}
/**
 * This file was auto-generated by `client-gen`.
 * Do not make direct changes to the file.
 */
{{- if (not IsClientOnly)}}
import { Context } from "../common/context.js"
{{- else }}
import { Context, connect as _connect, connection as _connection, ConnectOpts, CallbackFct } from "@dagger.io/dagger"
{{- end }}

{{ if IsClientOnly }}
export async function connection(
  fct: () => Promise<void>,
  cfg: ConnectOpts = {},
) {
  cfg.ServeCurrentModule = true

  const wrapperFunc = async (): Promise<void> => {
    {{ range $i, $dep := Dependencies -}}
    await dag.moduleSource("{{ $dep.Ref }}", { refPin: "{{ $dep.Pin }}" }).withName("{{ $dep.Name }}").asModule().serve()
    {{ end }}
    // Call the callback
    await fct()
  }

  return await _connection(wrapperFunc, cfg)
}

export async function connect(
  fct: CallbackFct,
  cfg: ConnectOpts = {},
) {
  cfg.ServeCurrentModule = true

  // Serve remote dependencies before calling the callback
  const wrapperFunc = async (client: Client): Promise<void> => {
    {{ range $i, $dep := Dependencies -}}
    await dag.moduleSource("{{ $dep.Ref }}", { refPin: "{{ $dep.Pin }}" }).withName("{{ $dep.Name }}").asModule().serve()
    {{ end }}

    // Call the callback with the client
    // This requires to use `any` to pass the type system
    await fct(client as any)
  }

  return await _connect(wrapperFunc as unknown as CallbackFct, cfg)
}
{{- end }}

/**
 * Declare a number as float in the Dagger API.
 */
export type float = number

class BaseClient {
  /**
   * @hidden
   */

  constructor(protected _ctx: Context = new Context()) {}
}
{{- end }}
