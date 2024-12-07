import { IntrospectionError } from "../../common/errors/index.js"
import { load } from "../entrypoint/load.js"
import { convertToPascalCase } from "./case_convertor.js"
import { DaggerModule } from "./dagger_module/index.js"
import { AST } from "./typescript_module/index.js"

export async function scan(files: string[], moduleName = "") {
  if (files.length === 0) {
    throw new IntrospectionError("no files to introspect found")
  }

  const formattedModuleName = convertToPascalCase(moduleName)
  const userModule = await load(files)

  // Interpret the given typescript source files.
  const ast = new AST(files, userModule)

  const module = new DaggerModule(formattedModuleName, userModule, ast)

  return module
}
