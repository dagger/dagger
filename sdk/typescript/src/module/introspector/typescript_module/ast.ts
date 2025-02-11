/* eslint-disable @typescript-eslint/no-explicit-any */
import Module from "node:module"
import * as path from "path"
import ts from "typescript"

import { TypeDefKind } from "../../../api/client.gen.js"
import { IntrospectionError } from "../../../common/errors/index.js"
import { DaggerDecorators } from "../dagger_module/index.js"
import { TypeDef } from "../typedef.js"
import { DeclarationsMap, isDeclarationOf } from "./declarations.js"
import { getValueByExportedName } from "./explorer.js"
import { Location } from "./location.js"

export const CLIENT_GEN_FILE = "client.gen.ts"

export type ResolvedNodeWithSymbol<T extends keyof DeclarationsMap> = {
  type: T
  node: DeclarationsMap[T]
  symbol: ts.Symbol
  file: ts.SourceFile
}

export class AST {
  public checker: ts.TypeChecker

  private readonly sourceFiles: ts.SourceFile[]

  constructor(
    public readonly files: string[],
    private readonly userModule: Module[],
  ) {
    const program = ts.createProgram(files, {
      experimentalDecorators: true,
      moduleResolution: ts.ModuleResolutionKind.Node10,
      target: ts.ScriptTarget.ES2022,
    })
    this.checker = program.getTypeChecker()
    this.sourceFiles = program
      .getSourceFiles()
      .filter((file) => !file.isDeclarationFile)
  }

  public findResolvedNodeByName<T extends keyof DeclarationsMap>(
    name: string,

    /**
     * Optionally look for a specific node kind if we already know
     * what we're looking for.
     */
    kind: T,
  ): ResolvedNodeWithSymbol<T> | undefined {
    let result: ResolvedNodeWithSymbol<T> | undefined

    for (const sourceFile of this.sourceFiles) {
      ts.forEachChild(sourceFile, (node) => {
        if (result !== undefined) return

        // Skip if it's not from the client gen nor the user module
        if (
          !sourceFile.fileName.endsWith(CLIENT_GEN_FILE) &&
          !this.files.includes(sourceFile.fileName)
        ) {
          return
        }

        if (kind !== undefined && node.kind === kind) {
          const isDeclarationValid = isDeclarationOf[kind](node)
          if (!isDeclarationValid) return

          const convertedNode = node as DeclarationsMap[typeof kind]
          if (!convertedNode.name || convertedNode.name.getText() !== name) {
            return
          }

          const symbol = this.checker.getSymbolAtLocation(convertedNode.name)
          if (!symbol) {
            console.debug(
              `missing symbol for ${name} at ${sourceFile.fileName}:${node.pos}`,
            )
            return
          }

          result = {
            type: kind,
            node: convertedNode,
            symbol: symbol,
            file: sourceFile,
          }
        }
      })
    }

    return result
  }

  public getTypeFromTypeAlias(typeAlias: ts.TypeAliasDeclaration): ts.Type {
    const symbol = this.getSymbolOrThrow(typeAlias.name)

    return this.checker.getDeclaredTypeOfSymbol(symbol)
  }

  public static getNodePosition(node: ts.Node): string {
    const sourceFile = node.getSourceFile()

    const position = ts.getLineAndCharacterOfPosition(
      sourceFile,
      node.getStart(),
    )

    return `${sourceFile.fileName}:${position.line}:${position.character}`
  }

  /**
   * Returns the location of the node in the source file.
   *
   * The filepath is relative to the module root directory.
   * Ideally, we use the identifier of the node accessible by node.name but fallback
   * to node itself if it's not available.
   *
   * The TypeScript SDK based it's line and column on index 0 but editors starts
   * at 1 so we always add 1 to fix that difference.
   */
  public static getNodeLocation(
    node: ts.Node & { name?: ts.Identifier },
  ): Location {
    const sourceFile = node.getSourceFile()

    // Use the identifier of the node if available.
    const targetNode = node.name ?? node

    const position = ts.getLineAndCharacterOfPosition(
      sourceFile,
      targetNode.getStart(sourceFile),
    )

    // sourceFile.fileName is the absolute path to the file, we need to get the relative path
    // from the module path so we exclude the module path from the given path.
    // But since root will always start with `/src`, we want to catch the second `src`
    // inside the module.
    const pathParts = sourceFile.fileName.split(path.sep)
    const srcIndex = pathParts.indexOf("src", 2)

    return {
      filepath: pathParts.slice(srcIndex).join(path.sep),
      line: position.line + 1,
      column: position.character + 1,
    }
  }

  public getDocFromSymbol(symbol: ts.Symbol): string {
    return ts.displayPartsToString(symbol.getDocumentationComment(this.checker))
  }

  public getSymbolOrThrow(node: ts.Node): ts.Symbol {
    const symbol = this.getSymbol(node)
    if (!symbol) {
      throw new IntrospectionError(
        `could not find symbol at ${AST.getNodePosition(node)}`,
      )
    }

    return symbol
  }

  public getSignatureFromFunctionOrThrow(
    node: ts.SignatureDeclaration,
  ): ts.Signature {
    const signature = this.checker.getSignatureFromDeclaration(node)
    if (!signature) {
      throw new IntrospectionError(
        `could not find signature at ${AST.getNodePosition(node)}`,
      )
    }

    return signature
  }

  public getSymbol(node: ts.Node): ts.Symbol | undefined {
    return this.checker.getSymbolAtLocation(node)
  }

  public isNodeDecoratedWith(
    node: ts.HasDecorators,
    daggerDecorator: DaggerDecorators,
  ): boolean {
    const decorators = ts.getDecorators(node)
    if (!decorators) {
      return false
    }

    const decorator = decorators.find((d) =>
      d.expression.getText().startsWith(daggerDecorator),
    )
    if (!decorator) {
      return false
    }

    if (!ts.isCallExpression(decorator.expression)) {
      throw new IntrospectionError(
        `decorator at ${AST.getNodePosition(node)} should be a call expression, please use ${daggerDecorator}() instead.`,
      )
    }

    return true
  }

  public getDecoratorArgument<T>(
    node: ts.HasDecorators,
    daggerDecorator: DaggerDecorators,
    type: "string" | "object",
    position = 0,
  ): T | undefined {
    const decorators = ts.getDecorators(node)
    if (!decorators) {
      return undefined
    }

    const decorator = decorators.find((d) =>
      d.expression.getText().startsWith(daggerDecorator),
    )
    if (!decorator) {
      return undefined
    }

    const argument = (decorator.expression as ts.CallExpression).arguments[
      position
    ]
    if (!argument) {
      return undefined
    }

    switch (type) {
      case "string":
        return argument.getText() as T
      case "object":
        return eval(`(${argument.getText()})`)
    }
  }

  public unwrapTypeStringFromPromise(type: string): string {
    if (type.startsWith("Promise<")) {
      return type.slice("Promise<".length, -">".length)
    }

    if (type.startsWith("Awaited<")) {
      return type.slice("Awaited<".length, -">".length)
    }

    return type
  }

  public unwrapTypeStringFromArray(type: string): string {
    if (type.endsWith("[]")) {
      return type.replace("[]", "")
    }

    if (type.startsWith("Array<")) {
      return type.slice("Array<".length, -">".length)
    }

    return type
  }

  public stringTypeToUnwrappedType(type: string): string {
    type = this.unwrapTypeStringFromPromise(type)

    // If they are difference, that means we upwrapped the array.
    const extractedTypeFromArray = this.unwrapTypeStringFromArray(type)
    if (extractedTypeFromArray !== type) {
      return this.stringTypeToUnwrappedType(extractedTypeFromArray)
    }

    return type
  }

  public typeToStringType(type: ts.Type): string {
    const stringType = this.checker.typeToString(type)

    return this.stringTypeToUnwrappedType(stringType)
  }

  public tsTypeToTypeDef(
    node: ts.Node,
    type: ts.Type,
  ): TypeDef<TypeDefKind> | undefined {
    if (type.flags & ts.TypeFlags.String)
      return { kind: TypeDefKind.StringKind }
    if (type.flags & ts.TypeFlags.Number) {
      // Float will be interpreted as number by the TypeScript compiler so we need to check if the
      // text is "float" to know if it's a float or an integer.
      // It can also be interpreted as a reference, but this is handled separately at an upper level.
      if (node.getText().includes("float")) {
        return { kind: TypeDefKind.FloatKind }
      }

      return { kind: TypeDefKind.IntegerKind }
    }
    if (type.flags & ts.TypeFlags.Boolean)
      return { kind: TypeDefKind.BooleanKind }
    if (type.flags & ts.TypeFlags.Void) return { kind: TypeDefKind.VoidKind }

    // If a type has a flag Object, is can basically be anything.
    // We firstly wants to see if it's a promise or an array so we can unwrap the
    // actual type.
    if (type.flags & ts.TypeFlags.Object) {
      const objectType = type as ts.ObjectType

      // If it's a reference, that means it's a generic type like
      // `Promise<T>` or `number[]` or `Array<T>`.
      if (objectType.objectFlags & ts.ObjectFlags.Reference) {
        const typeArguments = this.checker.getTypeArguments(
          type as ts.TypeReference,
        )

        switch (typeArguments.length) {
          case 0:
            // Might change after to support more complex type
            break
          case 1: {
            const typeArgument = typeArguments[0]

            if (type.symbol.getName() === "Promise") {
              return this.tsTypeToTypeDef(node, typeArgument)
            }

            if (type.symbol.getName() === "Array") {
              return {
                kind: TypeDefKind.ListKind,
                typeDef: this.tsTypeToTypeDef(node, typeArgument),
              }
            }

            return undefined
          }
          default: {
            throw new IntrospectionError(
              `could not resolve type ${type.symbol.getName()} at ${AST.getNodePosition(node)}, dagger does not support generics with argument yet.`,
            )
          }
        }
      }
    }
  }

  private resolveParameterDefaultValueTypeReference(
    expression: ts.Expression,
    value: any,
  ): any {
    const type = typeof value

    switch (type) {
      case "string":
      case "number": // float is also included here
      case "bigint":
      case "boolean":
      case "object":
        // Value will be jsonified on registration.
        return value
      default:
        // If we cannot resolve the value, we skip it and let the value be resolved automatically by the runtime
        return undefined
    }
  }

  public resolveParameterDefaultValue(expression: ts.Expression): any {
    const kind = expression.kind

    switch (kind) {
      case ts.SyntaxKind.StringLiteral:
        return `${eval(expression.getText())}`
      case ts.SyntaxKind.NumericLiteral:
        return parseInt(expression.getText())
      case ts.SyntaxKind.TrueKeyword:
        return true
      case ts.SyntaxKind.FalseKeyword:
        return false
      case ts.SyntaxKind.NullKeyword:
        return null
      case ts.SyntaxKind.ArrayLiteralExpression:
        return eval(expression.getText())
      case ts.SyntaxKind.Identifier: {
        // If the parameter is a reference to a variable, we try to resolve it using
        // exported modules value.
        const value = getValueByExportedName(
          expression.getText(),
          this.userModule,
        )

        if (value === undefined) {
          throw new IntrospectionError(
            `could not resolve default value reference to the variable: '${expression.getText()}' from ${AST.getNodePosition(expression)}. Is it exported by the module?`,
          )
        }

        return this.resolveParameterDefaultValueTypeReference(expression, value)
      }
      case ts.SyntaxKind.PropertyAccessExpression: {
        const accessors = expression.getText().split(".")

        let value = getValueByExportedName(accessors[0], this.userModule)
        for (let i = 1; i < accessors.length; i++) {
          value = value[accessors[i]]
        }

        return this.resolveParameterDefaultValueTypeReference(expression, value)
      }
      default: {
        console.warn(
          `default value '${expression.getText()}' at ${AST.getNodePosition(expression)} cannot be resolved, dagger does not support object or function as default value. 
          The value will be ignored by the introspection and resolve at the runtime.`,
        )
      }
    }
  }
}
