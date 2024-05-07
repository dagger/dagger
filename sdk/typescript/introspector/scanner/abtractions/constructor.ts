import ts from "typescript"

import { UnknownDaggerError } from "../../../common/errors/UnknownDaggerError.js"
import { ConstructorTypeDef, FunctionArgTypeDef } from "../typeDefs.js"
import { Argument, Arguments } from "./argument.js"

export class Constructor {
  private checker: ts.TypeChecker

  private declaration: ts.ConstructorDeclaration

  constructor(checker: ts.TypeChecker, declaration: ts.ConstructorDeclaration) {
    this.checker = checker
    this.declaration = declaration
  }

  get name(): string {
    return ""
  }

  get arguments(): Arguments {
    return this.declaration.parameters.reduce((acc: Arguments, param) => {
      const symbol = this.checker.getSymbolAtLocation(param.name)
      if (!symbol) {
        throw new UnknownDaggerError(
          `could not get constructor param: ${param.name.getText()}`,
          {},
        )
      }

      const argument = new Argument(this.checker, symbol)

      acc[argument.name] = argument

      return acc
    }, {})
  }

  // TODO(TomChv): replace with `ToJson` method
  // after the refactor is complete.
  get typeDef(): ConstructorTypeDef {
    return {
      args: Object.entries(this.arguments).reduce(
        (acc: { [name: string]: FunctionArgTypeDef }, [name, arg]) => {
          acc[name] = arg.typeDef

          return acc
        },
        {},
      ),
    }
  }

  toJSON() {
    return {
      args: this.arguments,
    }
  }

  getArgOrder(): string[] {
    return Object.keys(this.arguments)
  }
}
