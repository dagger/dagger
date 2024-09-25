import ts from "typescript"

import { DaggerModule } from "./abtractions/module.js"

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
    throw new Error("no files to introspect found")
  }

  // Interpret the given typescript source files.
  const program = ts.createProgram(files, { experimentalDecorators: true })
  const checker = program.getTypeChecker()

  const module = new DaggerModule(checker, moduleName, program.getSourceFiles())
  if (Object.keys(module.objects).length === 0) {
    throw new Error("no objects found in the module")
  }

  return module
}
