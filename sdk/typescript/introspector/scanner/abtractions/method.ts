import ts from "typescript"

import { UnknownDaggerError } from "../../../common/errors/UnknownDaggerError.js"
import { Argument } from "./argument.js"
import { serializeType } from "../serialize.js"
import { typeNameToTypedef } from "../utils.js"
import { TypeDefKind } from "../../../api/client.gen.js"
import { FunctionArgTypeDef, FunctionTypedef, TypeDef } from "../typeDefs.js"

const METHOD_DECORATOR = "func"

/**
 * Method is an abstraction of a function or method.
 *
 * This aims to simplify and adds clarity to how we analyse the code and using
 * clear accessor.
 */
export class Method {
  private checker: ts.TypeChecker

  private method: ts.MethodDeclaration

  private symbol: ts.Symbol

  private signature: ts.Signature

  private decorator: ts.Decorator | undefined

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
    this.method = method

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
   * Return the alias of the method if it has one.
   */
  get alias(): string | undefined {
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

  get arguments(): Argument[] {
    return this.signature.parameters.map((param) => {
      return new Argument(this.checker, param)
    })
  }

  /**
   * Return the type of the return value in a Dagger TypeDef format.
   */
  get returnType(): TypeDef<TypeDefKind> {
    return typeNameToTypedef(
      serializeType(this.checker, this.signature.getReturnType()),
    )
  }

  // TODO(TomChv): replace with `ToJson` method
  // after the refactor is complete.
  get typeDef(): FunctionTypedef {
    return {
      name: this.name,
      description: this.description,
      alias: this.alias,
      returnType: this.returnType,
      args: this.arguments.reduce(
        (acc: { [name: string]: FunctionArgTypeDef }, arg) => {
          acc[arg.name] = arg.typeDef

          return acc
        },
        {},
      ),
    }
  }
}
