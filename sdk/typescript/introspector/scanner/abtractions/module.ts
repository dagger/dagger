import ts from "typescript"

import { isObject, toPascalCase } from "../utils.js"
import { DaggerObject, DaggerObjects } from "./object.js"

export class DaggerModule {
  private checker: ts.TypeChecker

  private readonly files: ts.SourceFile[]

  public name: string

  constructor(
    checker: ts.TypeChecker,
    name = "",
    files: readonly ts.SourceFile[],
  ) {
    this.checker = checker
    this.files = files.filter((file) => !file.isDeclarationFile)
    this.name = toPascalCase(name)
  }

  get objects(): DaggerObjects {
    const objects: DaggerObjects = {}

    for (const file of this.files) {
      ts.forEachChild(file, (node) => {
        if (ts.isClassDeclaration(node) && isObject(node)) {
          const object = new DaggerObject(this.checker, file, node)

          objects[object.name] = object
        }
      })
    }

    return objects
  }

  get description(): string | undefined {
    const mainObject = Object.values(this.objects).find(
      (object) => object.name === this.name,
    )
    if (!mainObject) {
      return undefined
    }

    const file = mainObject.file
    const topLevelStatement = file.statements[0]
    if (!topLevelStatement) {
      return undefined
    }

    // Get the range of the top level comment
    const topLevelCommentRanges = ts.getLeadingCommentRanges(
      file.getFullText(),
      topLevelStatement.pos,
    )
    if (!topLevelCommentRanges || topLevelCommentRanges.length === 0) {
      return undefined
    }

    const topLevelCommentRange = topLevelCommentRanges[0]

    return file
      .getFullText()
      .substring(topLevelCommentRange.pos, topLevelCommentRange.end)
      .split("\n")
      .slice(1, -1) // Remove start and ending comments characters `/** */`
      .map((line) => line.replace("*", "").trim()) // Remove leading * and spaces
      .join("\n")
  }

  toJSON() {
    return {
      name: this.name,
      description: this.description,
      objects: Object.entries(this.objects).reduce(
        (acc: { [name: string]: DaggerObject }, [name, object]) => {
          acc[name] = object

          return acc
        },
        {},
      ),
    }
  }
}
