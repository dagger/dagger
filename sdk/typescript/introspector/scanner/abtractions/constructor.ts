import ts from "typescript"

import { UnknownDaggerError } from "../../../common/errors/UnknownDaggerError.js"
import { Argument, Arguments } from "./argument.js"

export class Constructor {
  private checker: ts.TypeChecker

  private declaration: ts.ConstructorDeclaration

  // Preloaded values.
  private _name: string = ""
  private _arguments: Arguments

  constructor(checker: ts.TypeChecker, declaration: ts.ConstructorDeclaration) {
    this.checker = checker
    this.declaration = declaration

    // Preload values.
    this._arguments = this.loadArguments()
  }

  get name(): string {
    return this._name
  }

  get arguments(): Arguments {
    return this._arguments
  }

  toJSON() {
    return {
      args: this.arguments,
    }
  }

  getArgOrder(): string[] {
    return Object.keys(this.arguments)
  }

  private loadArguments(): Arguments {
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
}
