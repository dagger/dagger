{{- define "entrypoint_helpers" -}}
function formatError(e: unknown): DaggerError {
  if (e instanceof Error) {
    let error = dag.error(e.message)
    const ext = (e as { extensions?: Record<string, unknown> }).extensions
    if (ext) {
      for (const [k, v] of Object.entries(ext)) {
        if (v !== "" && v !== undefined && v !== null) {
          error = error.withValue(
            k,
            JSON.stringify(v) as string & { __JSON: never },
          )
        }
      }
    }
    return error
  }
  try {
    return dag.error(JSON.stringify(e))
  } catch {
    return dag.error(String(e))
  }
}
{{- end -}}
