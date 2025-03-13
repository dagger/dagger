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
async function serveModuleDependencies(client: Client): Promise<void> {
  {{ range $i, $dep := Dependencies -}}
  await client.moduleSource("{{ $dep.Ref }}", { refPin: "{{ $dep.Pin }}" }).withName("{{ $dep.Name }}").asModule().serve()
  {{ end }}

  const modSrc = await client.moduleSource(".")
  const configExist = await modSrc.configExists()
  if (!configExist) {
    return
  }

  const dependencies = await modSrc.dependencies()
  await Promise.all(dependencies.map(async (dep) => {
    const kind = await dep.kind()
    if (kind === ModuleSourceKind.GitSource) {
      return
    }

    await dep.asModule().serve()
  }))

  const sdkSource = await modSrc.sdk().source()
  if (sdkSource !== null && sdkSource !== "") {
    await modSrc.asModule().serve()
  }
}

export async function connection(
  fct: () => Promise<void>,
  cfg: ConnectOpts = {},
) {
  const wrapperFunc = async (): Promise<void> => {
    await serveModuleDependencies(dag)

    // Call the callback
    await fct()
  }

  return await _connection(wrapperFunc, cfg)
}

export async function connect(
  fct: CallbackFct,
  cfg: ConnectOpts = {},
) {
  // Serve remote dependencies before calling the callback
  const wrapperFunc = async (client: Client): Promise<void> => {
    await serveModuleDependencies(client)

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
