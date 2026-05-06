{{- define "entrypoint_dispatch" -}}
{{- $module := . -}}
async function invoke(
  parentName: string,
  fnName: string,
  parentJson: any,
  args: Record<string, any>,
): Promise<any> {
  switch (parentName) {
{{- range $name := sortedKeysObjects $module.Objects }}
{{- $obj := index $module.Objects $name }}
    case {{ jsString $obj.Name }}: {
      switch (fnName) {
{{ template "entrypoint_constructor_case" (dict "Obj" $obj) }}
{{- range $mName := sortedKeysMethods $obj.Methods }}
{{- $fn := index $obj.Methods $mName }}
{{ template "entrypoint_method_case" (dict "Obj" $obj "Fn" $fn) }}
{{- end }}
        default:
          throw new Error(`unknown function ${fnName} on {{ $obj.Name }}`)
      }
    }
{{- end }}
    default:
      throw new Error(`unknown object ${parentName}`)
  }
}

async function dispatch() {
  await connection(async () => {
    const fnCall = dag.currentFunctionCall()
    const parentName = await fnCall.parentName()

    if (parentName === "") {
      const id = await register()
      await fnCall.returnValue(JSON.stringify(id) as string & { __JSON: never })
      return
    }

    const fnName = await fnCall.name()
    const parentJson = JSON.parse(await fnCall.parent())
    const fnArgs = await fnCall.inputArgs()

    const args: Record<string, any> = {}
    for (const arg of fnArgs) {
      args[await arg.name()] = JSON.parse(await arg.value())
    }

    try {
      const result = await invoke(parentName, fnName, parentJson, args)
      const out = result === undefined || result === null ? "null" : JSON.stringify(result)
      await fnCall.returnValue(out as string & { __JSON: never })
    } catch (e: unknown) {
      await fnCall.returnError(formatError(e))
      process.exit(1)
    }
  }, { LogOutput: process.stdout })
}
{{- end -}}

{{- define "entrypoint_constructor_case" -}}
{{- $obj := .Obj -}}
{{- if $obj.Constructor }}
        case "": {
{{- range $arg := $obj.Constructor.Arguments }}
          {{ argCoercionLine $arg }}
{{- end }}
          const __result = await new {{ classRuntimeRef $obj }}(
            {{- range $i, $arg := $obj.Constructor.Arguments -}}
            {{- if $i }}, {{ end }}{{ coercedVarName $arg }}
            {{- end -}}
          ) as unknown as {{ classTypeRef $obj }}
          return await serialize{{ $obj.Name }}(__result)
        }
{{- else if eq $obj.Kind "class" }}
        case "": {
          const __result = new {{ classRuntimeRef $obj }}()
          return await serialize{{ $obj.Name }}(__result)
        }
{{- else }}
        case "": {
          return await serialize{{ $obj.Name }}({} as any)
        }
{{- end -}}
{{- end -}}

{{- define "entrypoint_method_case" -}}
{{- $obj := .Obj -}}
{{- $fn := .Fn -}}
{{- $caseName := $fn.Name -}}
{{- if $fn.Alias }}{{ $caseName = $fn.Alias }}{{ end }}
        case {{ jsString $caseName }}: {
          const __parent = rebuild{{ $obj.Name }}(parentJson)
{{- range $arg := $fn.Arguments }}
          {{ argCoercionLine $arg }}
{{- end }}
          const __result = await __parent.{{ $fn.Name }}(
            {{- range $i, $arg := $fn.Arguments -}}
            {{- if $i }}, {{ end -}}
            {{- if $arg.IsVariadic }}...{{ end }}{{ coercedVarName $arg -}}
            {{- end -}}
          )
{{- if $fn.ReturnType }}
{{- if isInteger $fn.ReturnType }}
          if (typeof __result === "number" && __result % 1 !== 0) {
            throw new Error(`cannot return float '${__result}' if return type is 'number' (integer), please use 'float' as return type instead`)
          }
{{- end }}
          return {{ serializeExpr "__result" $fn.ReturnType }}
{{- else }}
          return null
{{- end }}
        }
{{- end -}}
