import ts from "typescript"

import { TypeDefKind } from "../../../api/client.gen.js"
import { UnknownDaggerError } from "../../../common/errors/UnknownDaggerError.js"
import { TypeDef } from "../typeDefs.js"
import { typeToTypedef } from "./typeToTypedef.js"

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

  // Preloaded values.
  private _name: string
  private _description: string
  private _type: TypeDef<TypeDefKind>
  private _defaultValue: string | undefined
  private _isOptional: boolean
  private _isNullable: boolean
  private _isVariadic: boolean

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

    // Preload to optimize the introspection.
    this._name = this.loadName()
    this._description = this.loadDescription()
    this._type = this.loadType()
    this._defaultValue = this.loadDefaultValue()
    this._isNullable = this.loadIsNullable()
    this._isVariadic = this.loadIsVariadic()
    this._isOptional = this.loadIsOptional()
  }

  get name(): string {
    return this._name
  }

  get description(): string {
    return this._description
  }

  /**
   * Return the type of the argument in a Dagger TypeDef format.
   */
  get type(): TypeDef<TypeDefKind> {
    return this._type
  }

  get defaultValue(): string | undefined {
    return this._defaultValue
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
    return this._isOptional
  }

  /**
   * Return true if the parameter is nullable.
   *
   * A parameter is considered nullable if itstype is a union type with `null`
   * on the list of types.
   * Example: `foo: string | null`.
   */
  get isNullable(): boolean {
    return this._isNullable
  }

  get isVariadic(): boolean {
    return this._isVariadic
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

  private loadName(): string {
    return this.symbol.getName()
  }

  private loadDescription(): string {
    return ts.displayPartsToString(
      this.symbol.getDocumentationComment(this.checker),
    )
  }

  private loadType(): TypeDef<TypeDefKind> {
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

  private loadDefaultValue(): string | undefined {
    if (this.param.initializer === undefined) {
      return undefined
    }

    return this.formatDefaultValue(this.param.initializer.getText())
  }

  private loadIsOptional(): boolean {
    return (
      this.param.questionToken !== undefined ||
      this.isVariadic ||
      this.isNullable
    )
  }

  private loadIsNullable(): boolean {
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

  private loadIsVariadic(): boolean {
    return this.param.dotDotDotToken !== undefined
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
