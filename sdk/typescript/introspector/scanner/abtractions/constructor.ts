import ts from "typescript"
import { UnknownDaggerError } from "../../../common/errors/UnknownDaggerError.js"
import { Argument } from "./argument.js"
import { ConstructorTypeDef } from "../typeDefs.js"

export class Constructor {
  private checker: ts.TypeChecker

  private declaration: ts.ConstructorDeclaration

  constructor(checker: ts.TypeChecker, declaration: ts.ConstructorDeclaration) {
    this.checker = checker
    this.declaration = declaration
  }

  get arguments(): Argument[] {
    return this.declaration.parameters.map((param) => {
      const symbol = this.checker.getSymbolAtLocation(param.name)
      if (!symbol) {
        throw new UnknownDaggerError(
          `could not get constructor param: ${param.name.getText()}`,
          {},
        )
      }

      return new Argument(this.checker, symbol)
    })
  }

  // TODO(TomChv): replace with `ToJson` method
  // after the refactor is complete.
  get typeDef(): ConstructorTypeDef {
    return {
      args: this.arguments.reduce(
        (acc, arg) => ({
          ...acc,
          [arg.name]: arg.typeDef,
        }),
        {},
      ),
    }
  }
}
