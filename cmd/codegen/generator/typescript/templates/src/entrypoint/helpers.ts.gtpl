{{- define "entrypoint_helpers" -}}
// Load a core/dependency object from its ID via node(id:) and wrap it in the
// matching generated client class. Mirrors the SDK runtime loader; replaces the
// retired load<Type>FromID API (removed in #12041). Some core type names
// collide with JS builtins and get a trailing "_" (e.g. "Module" -> Module_).
function __loadCoreObject(id: string, typeName: string): any {
  const cls =
    (__dagger as any)[typeName] ?? (__dagger as any)[typeName + "_"]
  if (!cls) {
    throw new Error(`generated client class not found for core type: ${typeName}`)
  }
  return new cls(new Context().selectNode(id, typeName))
}

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
