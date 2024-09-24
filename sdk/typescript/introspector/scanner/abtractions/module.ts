import ts from "typescript"

import { DaggerEnum, DaggerEnums, isEnumDecorated } from "./enum.js"
import { DaggerObject, DaggerObjects, isObjectDecorated } from "./object.js"

export class DaggerModule {
  private checker: ts.TypeChecker

  private readonly files: ts.SourceFile[]

  public name: string

  // Preloaded values.
  private _description: string | undefined
  private _objects: DaggerObjects
  private _enums: DaggerEnums

  constructor(
    checker: ts.TypeChecker,
    name = "",
    files: readonly ts.SourceFile[],
  ) {
    this.checker = checker
    this.files = files.filter((file) => !file.isDeclarationFile)
    this.name = this.toPascalCase(name)

    // Preload values to optimize introspection.
    this._objects = this.loadObjects()
    this._enums = this.loadEnums()
    this._description = this.loadDescription()
  }

  get objects(): DaggerObjects {
    return this._objects
  }

  get enums(): DaggerEnums {
    return this._enums
  }

  get description(): string | undefined {
    return this._description
  }

  toJSON() {
    return {
      name: this.name,
      description: this.description,
      objects: this._objects,
      enums: this._enums,
    }
  }

  private loadObjects(): DaggerObjects {
    const objects: DaggerObjects = {}

    for (const file of this.files) {
      ts.forEachChild(file, (node) => {
        if (ts.isClassDeclaration(node) && isObjectDecorated(node)) {
          const object = new DaggerObject(this.checker, file, node)

          objects[object.name] = object
        }
      })
    }

    return objects
  }

  private loadEnums(): DaggerEnums {
    const daggerEnums: DaggerEnums = {}

    for (const file of this.files) {
      ts.forEachChild(file, (node) => {
        if (ts.isClassDeclaration(node) && isEnumDecorated(node)) {
          const daggerEnum = new DaggerEnum(this.checker, file, node)

          daggerEnums[daggerEnum.name] = daggerEnum
        }
      })
    }

    return daggerEnums
  }

  private loadDescription(): string | undefined {
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

  private toPascalCase(input: string): string {
    const words = input
      .replace(/[^a-zA-Z0-9]/g, " ") // Replace non-alphanumeric characters with spaces
      .split(/\s+/)
      .filter((word) => word.length > 0)

    if (words.length === 0) {
      return "" // No valid words found
    }

    // It's an edge case when moduleName is already in PascalCase or camelCase
    if (words.length === 1) {
      return words[0].charAt(0).toUpperCase() + words[0].slice(1)
    }

    const pascalCase = words
      .map((word) => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
      .join("")

    return pascalCase
  }
}
