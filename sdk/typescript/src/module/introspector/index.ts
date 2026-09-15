import Module from "node:module"

import { IntrospectionError } from "../../common/errors/index.js"
import { load } from "../entrypoint/load.js"
import { convertToPascalCase } from "./case_convertor.js"
import { DaggerModule } from "./dagger_module/index.js"
import { AST } from "./typescript_module/index.js"

export async function scan(
  files: string[],
  moduleName = "",
  loadModule = true,
  // generatedClientFiles is the explicit set of SDK-generated binding files
  // (client.gen.ts and each <dep>.gen.ts). When provided, the AST uses it to
  // tell generated code from user source instead of guessing by filename,
  // which would misclassify a user file that happens to end in `.gen.ts`.
  generatedClientFiles: string[] = [],
) {
  if (files.length === 0) {
    throw new IntrospectionError("no files to introspect found")
  }

  const formattedModuleName = convertToPascalCase(moduleName)

  // Interpret the given typescript source files.
  let userModule: Module[] = []
  if (loadModule) {
    userModule = await load(files)
  }
  const ast = new AST(files, userModule, generatedClientFiles)

  const module = new DaggerModule(formattedModuleName, userModule, ast)

  return module
}
