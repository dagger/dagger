package tsutils

import "fmt"

// This file directly embeds the static files content in the SDK so we don't have to
// fetch them from current module source.
// This is done to avoid having to fetch the SDK module's filesystem which can take up
// to 1s to load.
// We can afford to embed them since they're very unlikely to change.

// StaticBundleTelemetryTS is the content of the sdk/index.ts file.
const StaticBundleIndexTS = `export {
  connection,
  connect,
  Context,
  func,
  argument,
  object,
  field,
  enumType,
  entrypoint,
} from "./core.js"

export type { ConnectOpts, CallbackFct } from "./core.js"

export * from "./client.gen.js"
`

// StaticBundleTelemetryTS is the content of the sdk/telemetry.ts file.
const StaticBundleTelemetryTS = `import { getTracer } from "./core.js"

export { getTracer }
`

// StaticDefaultPackageJSON is the default content of the package.json file.
var StaticDefaultPackageJSON = `{
  "type": "module"
}`

// StaticEntrypoint is the content of the __dagger.entrypoint.ts file.
var StaticEntrypointTS = fmt.Sprintf(`// THIS FILE IS AUTO GENERATED. PLEASE DO NOT EDIT.
import { entrypoint } from "@dagger.io/dagger"
import * as fs from "fs"
import * as path from "path"

const allowedExtensions = [".ts", ".mts"]

function listTsFilesInModule(dir = import.meta.dirname): string[] {
  let bundle = true

  // For background compatibility, if there's a package.json in the sdk directory
  // We should set the right path to the client.
  if (fs.existsSync(%s)) {
    bundle = false
  }

  const res = fs.readdirSync(dir).map((file) => {
    const filepath = path.join(dir, file)

    const stat = fs.statSync(filepath)

    if (stat.isDirectory()) {
      return listTsFilesInModule(filepath)
    }

    const ext = path.extname(filepath)
    if (allowedExtensions.find((allowedExt) => allowedExt === ext)) {
      return [path.join(dir, file)]
    }

    return []
  })

  return res.reduce(
    (p, c) => [...c, ...p],
    [%s],
  )
}

const files = listTsFilesInModule()

entrypoint(files)
`,
	"`${import.meta.dirname}/../sdk/package.json`",
	"`${import.meta.dirname}/../sdk/${bundle ? \"\" : \"src/api/\"}client.gen.ts`",
)
