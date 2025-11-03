// THIS FILE IS AUTO GENERATED. PLEASE DO NOT EDIT.
import { entrypoint } from "@dagger.io/dagger"
import * as fs from "fs"
import * as path from "path"

const allowedExtensions = [".ts", ".mts"]

function listTsFilesInModule(dir = import.meta.dirname): string[] {
  let bundle = true

  // For background compatibility, if there's a package.json in the sdk directory
  // We should set the right path to the client.
  if (fs.existsSync(`${import.meta.dirname}/../sdk/package.json`)) {
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
    [`${import.meta.dirname}/../sdk/${bundle ? "" : "src/api/"}client.gen.ts`],
  )
}

const files = listTsFilesInModule()

entrypoint(files)
