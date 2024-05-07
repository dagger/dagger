import ts from "typescript"

import { TypeDefKind } from "../../../api/client.gen.js"
import { UnknownDaggerError } from "../../../common/errors/UnknownDaggerError.js"
import { FunctionArgTypeDef, TypeDef } from "../typeDefs.js"
import { typeToTypedef } from "../utils.js"

export type Arguments = { [name: string]: Argument }

/**
 * Argument is an abstraction of a function argument.
 *
 * This aims to simplify and adds clarity to how we analyse the code and using
 * clear accessor.
 */
export class Argument {
  private symbol: ts.Symbol

  private checker: ts.TypeChecker

  private param: ts.ParameterDeclaration

  /**
   * Create a new Argument instance.
   *
   * @param checker Checker to use to introspect the type of the argument.
   * @param param The symbol of the argument to introspect.
   *
   * @throws UnknownDaggerError If the symbol doesn't have any declaration.
   * @throws UnknownDaggerError If the declaration of the symbol isn't a parameter.
   */
  constructor(checker: ts.TypeChecker, param: ts.Symbol) {
    this.symbol = param
    this.checker = checker

    const declarations = this.symbol.getDeclarations()
    if (!declarations || declarations.length < 0) {
      throw new UnknownDaggerError(
        `could not find param declarations of symbol ${this.symbol.name}`,
        {},
      )
    }

    const parameterDeclaration = declarations[0]
    if (!ts.isParameter(parameterDeclaration)) {
      throw new UnknownDaggerError(
        `the declaration of symbol ${this.symbol.name} isn't a parameter`,
        {},
      )
    }

    this.param = parameterDeclaration
  }

  get name(): string {
    return this.symbol.getName()
  }

  get description(): string {
    return ts.displayPartsToString(
      this.symbol.getDocumentationComment(this.checker),
    )
  }

  /**
   * Return the type of the argument in a Dagger TypeDef format.
   */
  get type(): TypeDef<TypeDefKind> {
    if (!this.symbol.valueDeclaration) {
      throw new UnknownDaggerError(
        "could not find symbol value declaration",
        {},
      )
    }

    const type = this.checker.getTypeOfSymbolAtLocation(
      this.symbol,
      this.symbol.valueDeclaration,
    )

    return typeToTypedef(this.checker, type)
  }

  get defaultValue(): string | undefined {
    if (this.param.initializer === undefined) {
      return undefined
    }

    return this.formatDefaultValue(this.param.initializer.getText())
  }

  /**
   * Return true if the parameter is optional.
   *
   * A parameter is considered optional if he fits one of the following:
   * - It has a question token (e.g. `foo?: <type>`).
   * - It's variadic (e.g. `...foo: <type>[]`).
   * - It's nullable (e.g. `foo: <type> | null`).
   */
  get isOptional(): boolean {
    return (
      this.param.questionToken !== undefined ||
      this.isVariadic ||
      this.isNullable
    )
  }

  /**
   * Return true if the parameter is nullable.
   *
   * A parameter is considered nullable if itstype is a union type with `null`
   * on the list of types.
   * Example: `foo: string | null`.
   */
  get isNullable(): boolean {
    if (!this.param.type) {
      return false
    }

    if (ts.isUnionTypeNode(this.param.type)) {
      for (const _type of this.param.type.types) {
        if (_type.getText() === "null") {
          return true
        }
      }
    }

    return false
  }

  get isVariadic(): boolean {
    return this.param.dotDotDotToken !== undefined
  }

  // TODO(TomChv): replace with `ToJson` method
  // after the refactor is complete.
  get typeDef(): FunctionArgTypeDef {
    return {
      name: this.name,
      description: this.description,
      optional: this.isOptional,
      defaultValue: this.defaultValue,
      isVariadic: this.isVariadic,
      typeDef: this.type,
    }
  }

  toJSON() {
    return {
      name: this.name,
      description: this.description,
      type: this.type,
      isVariadic: this.isVariadic,
      isNullable: this.isNullable,
      isOptional: this.isOptional,
      defaultValue: this.defaultValue,
    }
  }

  /**
   * The TypeScript Compiler API returns the raw default value as it is written
   * by the user.
   * However, some notations are not supported by GraphQL so this function
   * formats the default value to be compatible with the GraphQL syntax.
   *
   * Formatting rules:
   * - Single quote strings are converted to double quote strings.
   *
   * @param value The value to format.
   */
  private formatDefaultValue(value: string): string {
    const isSingleQuoteString = (): boolean =>
      value.startsWith("'") && value.endsWith("'")

    if (isSingleQuoteString()) {
      return `"${value.slice(1, value.length - 1)}"`
    }

    return value
  }
}
