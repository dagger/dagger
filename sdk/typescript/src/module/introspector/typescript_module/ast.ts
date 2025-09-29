/* eslint-disable @typescript-eslint/no-explicit-any */
import Module from "node:module"
import * as path from "path"
import ts from "typescript"

import { TypeDefKind } from "../../../api/client.gen.js"
import { IntrospectionError } from "../../../common/errors/index.js"
import { DaggerDecorators } from "../dagger_module/index.js"
import { TypeDef } from "../typedef.js"
import { DeclarationsMap, isDeclarationOf } from "./declarations.js"
import { Location } from "./location.js"

export const CLIENT_GEN_FILE = "client.gen.ts"

export type ResolvedNodeWithSymbol<T extends keyof DeclarationsMap> = {
  type: T
  node: DeclarationsMap[T]
  symbol: ts.Symbol
  file: ts.SourceFile
}

export type SymbolDoc = {
  description: string
  deprecated?: string
}

export class AST {
  public checker: ts.TypeChecker

  private readonly sourceFiles: ts.SourceFile[]

  constructor(
    public readonly files: string[],
    private readonly userModule: Module[],
  ) {
    this.files = files.map((f) => path.resolve(f))
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
          !this.files.includes(path.resolve(sourceFile.fileName))
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

  public findAllDeclarations<T extends keyof DeclarationsMap>(
    kind: T,
  ): ResolvedNodeWithSymbol<T>[] {
    const results: ResolvedNodeWithSymbol<T>[] = []

    for (const sourceFile of this.sourceFiles) {
      ts.forEachChild(sourceFile, (node) => {
        // Skip if it's not from the client gen nor the user module
        if (
          !sourceFile.fileName.endsWith(CLIENT_GEN_FILE) &&
          !this.files.includes(path.resolve(sourceFile.fileName))
        ) {
          return
        }

        if (kind !== undefined && node.kind === kind) {
          const isDeclarationValid = isDeclarationOf[kind](node)
          if (!isDeclarationValid) return

          const convertedNode = node as DeclarationsMap[typeof kind]
          if (!convertedNode.name) {
            return
          }

          const symbol = this.checker.getSymbolAtLocation(convertedNode.name)
          if (!symbol) {
            console.debug(
              `missing symbol for ${convertedNode.name.getText()} at ${sourceFile.fileName}:${node.pos}`,
            )
            return
          }

          results.push({
            type: kind,
            node: convertedNode,
            symbol: symbol,
            file: sourceFile,
          })
        }
      })
    }

    return results
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
    const pathParts = path.resolve(sourceFile.fileName).split(path.sep)
    const srcIndex = pathParts.indexOf("src", 2)

    return {
      filepath: pathParts.slice(srcIndex).join(path.sep),
      line: position.line + 1,
      column: position.character + 1,
    }
  }

  public getDocFromSymbol(symbol: ts.Symbol): string {
    return this.getSymbolDoc(symbol).description
  }

  public getSymbolDoc(symbol: ts.Symbol): SymbolDoc {
    const description = ts
      .displayPartsToString(symbol.getDocumentationComment(this.checker))
      .trim()

    let deprecated: string | undefined
    let hasDeprecatedTag = false

    for (const tag of symbol.getJsDocTags()) {
      if (tag.name !== "deprecated") continue

      hasDeprecatedTag = true
      const text =
        tag.text?.map((part) => ("text" in part ? part.text : part)).join("") ??
        ""
      deprecated = text.trim()
      break
    }

    if (!hasDeprecatedTag) {
      return { description }
    }

    return {
      description,
      deprecated: deprecated ?? "",
    }
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

  private warnUnresolvedDefaultValue(expression: ts.Expression): void {
    console.warn(
      `default value '${expression.getText()}' at ${AST.getNodePosition(expression)} cannot be resolved, dagger does not support object or function as default value. 
          The value will be ignored by the introspection and resolve at the runtime.`,
    )
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
        const symbol = this.checker.getSymbolAtLocation(expression)
        if (!symbol) {
          throw new IntrospectionError(
            `could not resolve default value reference to the variable: '${expression.getText()}' from ${AST.getNodePosition(expression)}. Is it exported by the module?`,
          )
        }

        // Parse the default value from the variable declaration
        // ```
        // export const foo = "A"
        //
        // function bar(baz: string = foo) {}
        // ```
        const decl = symbol.valueDeclaration ?? symbol.declarations?.[0]
        if (!decl) {
          this.warnUnresolvedDefaultValue(expression)

          return undefined
        }

        if (ts.isVariableDeclaration(decl) && decl.initializer) {
          return this.resolveParameterDefaultValue(decl.initializer)
        }

        // Parse the default value from the enum member
        // ```
        // enum Foo {
        //   A = "a"
        // }
        //
        // function bar(baz: string = Foo.A) {}
        // ```
        if (ts.isEnumMember(decl)) {
          const val = this.checker.getConstantValue(decl)
          if (val !== undefined) return val
          if (decl.initializer)
            return this.resolveParameterDefaultValue(decl.initializer)
        }

        // Parse the default value from the import specifier
        // ```
        // import { foo } from "bar"
        //
        // function bar(baz: string = foo) {}
        // ```
        if (ts.isImportSpecifier(decl)) {
          const aliased = this.checker.getAliasedSymbol(symbol)
          const aliasedDecl =
            aliased?.valueDeclaration ?? aliased?.declarations?.[0]
          if (
            aliasedDecl &&
            ts.isVariableDeclaration(aliasedDecl) &&
            aliasedDecl.initializer
          ) {
            return this.resolveParameterDefaultValue(aliasedDecl.initializer)
          }
        }

        // Warn the user if the default value cannot be resolved
        this.warnUnresolvedDefaultValue(expression)

        return undefined
      }
      case ts.SyntaxKind.PropertyAccessExpression: {
        const symbol = this.checker.getSymbolAtLocation(expression)
        if (!symbol) return undefined

        const decl = symbol.valueDeclaration
        if (!decl) {
          this.warnUnresolvedDefaultValue(expression)

          return undefined
        }

        if (ts.isEnumMember(decl)) {
          const val = this.checker.getConstantValue(decl)
          if (val !== undefined) return val

          if (decl.initializer)
            return this.resolveParameterDefaultValue(decl.initializer)
        }

        // Warn the user if the default value cannot be resolved
        this.warnUnresolvedDefaultValue(expression)

        return undefined
      }
      default: {
        // Warn the user if the default value cannot be resolved
        this.warnUnresolvedDefaultValue(expression)
      }
    }
  }
}
