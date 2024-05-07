import ts from "typescript"

import { UnknownDaggerError } from "../../common/errors/UnknownDaggerError.js"
import { DaggerModule } from "./abtractions/module.js"
import { ClassTypeDef, FunctionTypedef } from "./typeDefs.js"

export type ScanResult = {
  module: {
    description?: string
  }
  classes: { [name: string]: ClassTypeDef }
  functions: { [name: string]: FunctionTypedef }
}

/**
 * Scan the list of TypeScript File using the TypeScript compiler API.
 *
 * This function introspect files and returns metadata of their class and
 * functions that should be exposed to the Dagger API.
 *
 * WARNING(28/11/23): This does NOT include arrow style function.
 *
 * @param files List of TypeScript files to introspect.
 * @param moduleName The name of the module to introspect.
 */
export function scan(files: string[], moduleName = ""): DaggerModule {
  if (files.length === 0) {
    throw new UnknownDaggerError("no files to introspect found", {})
  }

  // Interpret the given typescript source files.
  const program = ts.createProgram(files, { experimentalDecorators: true })
  const checker = program.getTypeChecker()

  return new DaggerModule(checker, moduleName, program.getSourceFiles())
}
