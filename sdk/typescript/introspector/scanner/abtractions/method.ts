import ts from "typescript"

import { TypeDefKind } from "../../../api/client.gen.js"
import { UnknownDaggerError } from "../../../common/errors/UnknownDaggerError.js"
import { func } from "../../decorators/decorators.js"
import { TypeDef } from "../typeDefs.js"
import { Argument, Arguments } from "./argument.js"
import { typeToTypedef } from "./typeToTypedef.js"

export const METHOD_DECORATOR = func.name

/**
 * Return true if the given method has the decorator @fct() on top
 * of its declaration.
 *
 * @param method The method to check
 */
export function isMethodDecorated(method: ts.MethodDeclaration): boolean {
  return (
    ts.getDecorators(method)?.find((d) => {
      if (ts.isCallExpression(d.expression)) {
        return d.expression.expression.getText() === METHOD_DECORATOR
      }

      return false
    }) !== undefined
  )
}

export type Methods = { [name: string]: Method }

/**
 * Method is an abstraction of a function or method.
 *
 * This aims to simplify and adds clarity to how we analyse the code and using
 * clear accessor.
 */
export class Method {
  private checker: ts.TypeChecker

  private symbol: ts.Symbol

  private signature: ts.Signature

  private decorator: ts.Decorator | undefined

  // Preloaded values.
  private _name: string
  private _description: string
  private _alias: string | undefined
  private _arguments: Arguments
  private _returnType: TypeDef<TypeDefKind>

  /**
   * Create a new Method instance.
   *
   * @param checker Checker to use to introspect the method.
   * @param method The method to introspect.
   *
   * @throws UnknownDaggerError If the method doesn't have any symbol.
   * @throws UnknownDaggerError If the method doesn't have any signature.
   */
  constructor(checker: ts.TypeChecker, method: ts.MethodDeclaration) {
    this.checker = checker

    const methodSymbol = checker.getSymbolAtLocation(method.name)
    if (!methodSymbol) {
      throw new UnknownDaggerError(
        `could not get method symbol: ${method.name.getText()}`,
        {},
      )
    }

    this.symbol = methodSymbol

    const signature = checker.getSignatureFromDeclaration(method)
    if (!signature) {
      throw new UnknownDaggerError(
        `could not get method signature: ${method.name.getText()}`,
        {},
      )
    }

    this.signature = signature

    this.decorator = ts.getDecorators(method)?.find((d) => {
      if (ts.isCallExpression(d.expression)) {
        return d.expression.expression.getText() === METHOD_DECORATOR
      }

      return false
    })

    // Preload to optimize the introspection.
    this._name = this.loadName()
    this._description = this.loadDescription()
    this._alias = this.loadAlias()
    this._arguments = this.loadArguments()
    this._returnType = this.loadReturnType()
  }

  get name(): string {
    return this._name
  }

  get description(): string {
    return this._description
  }

  /**
   * Return the alias of the method if it has one.
   */
  get alias(): string | undefined {
    return this._alias
  }

  get arguments(): Arguments {
    return this._arguments
  }

  /**
   * Return the type of the return value in a Dagger TypeDef format.
   */
  get returnType(): TypeDef<TypeDefKind> {
    return this._returnType
  }

  toJSON() {
    return {
      name: this.name,
      description: this.description,
      alias: this.alias,
      arguments: this.arguments,
      returnType: this.returnType,
    }
  }

  getArgOrder(): string[] {
    return Object.keys(this.arguments)
  }

  private loadName(): string {
    return this.symbol.getName()
  }

  private loadDescription(): string {
    return ts.displayPartsToString(
      this.symbol.getDocumentationComment(this.checker),
    )
  }

  private loadAlias(): string | undefined {
    if (!this.decorator) {
      return undefined
    }

    const expression = this.decorator.expression as ts.CallExpression
    const aliasArg = expression.arguments[0]

    if (!aliasArg) {
      return undefined
    }

    return JSON.parse(aliasArg.getText().replace(/'/g, '"'))
  }

  private loadArguments(): Arguments {
    return this.signature.parameters.reduce((acc: Arguments, param) => {
      const argument = new Argument(this.checker, param)

      acc[argument.name] = argument

      return acc
    }, {})
  }

  private loadReturnType(): TypeDef<TypeDefKind> {
    return typeToTypedef(this.checker, this.signature.getReturnType())
  }
}
