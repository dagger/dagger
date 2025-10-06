import ts from "typescript"

import { AST } from "../typescript_module/index.js"
import { DaggerArgument, DaggerArguments } from "./argument.js"
import { References } from "./reference.js"

export class DaggerConstructor {
  public name: string = ""
  public arguments: DaggerArguments = {}

  constructor(
    private readonly node: ts.ConstructorDeclaration,
    private readonly ast: AST,
  ) {
    const parameters = this.node.parameters

    for (const parameter of parameters) {
      this.arguments[parameter.name.getText()] = new DaggerArgument(
        parameter,
        this.ast,
      )
    }
  }

  public getArgsOrder(): string[] {
    return Object.keys(this.arguments)
  }

  public getReferences(): string[] {
    const references: string[] = []

    for (const argument of Object.values(this.arguments)) {
      const ref = argument.getReference()
      if (ref) {
        references.push(ref)
      }
    }

    return references
  }

  public propagateReferences(references: References) {
    for (const argument of Object.values(this.arguments)) {
      argument.propagateReferences(references)
    }
  }

  toJSON() {
    return {
      arguments: this.arguments,
    }
  }
}
