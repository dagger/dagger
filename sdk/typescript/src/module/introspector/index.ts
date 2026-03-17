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
  const ast = new AST(files, userModule)

  const module = new DaggerModule(formattedModuleName, userModule, ast)

  return module
}
