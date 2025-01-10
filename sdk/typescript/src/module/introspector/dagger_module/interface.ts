import ts from "typescript"

import { IntrospectionError } from "../../../common/errors/index.js"
import { AST } from "../typescript_module/index.js"
import {
  DaggerInterfaceFunction,
  DaggerInterfaceFunctions,
} from "./interfaceFunction.js"
import { Locatable } from "./locatable.js"
import { References } from "./reference.js"

export type DaggerInterfaces = { [name: string]: DaggerInterface }

export class DaggerInterface extends Locatable {
  public name: string
  public description: string
  public functions: DaggerInterfaceFunctions = {}
  private symbol: ts.Symbol

  constructor(
    private readonly node: ts.InterfaceDeclaration,
    private readonly ast: AST,
  ) {
    super(node)

    if (!this.node.name) {
      throw new IntrospectionError(
        `could not resolve name of interface at ${AST.getNodePosition(node)}`,
      )
    }
    this.name = this.node.name.getText()

    this.symbol = this.ast.getSymbolOrThrow(this.node.name)
    this.description = this.ast.getDocFromSymbol(this.symbol)

    for (const member of this.node.members) {
      if (!ts.isPropertySignature(member) && !ts.isMethodSignature(member)) {
        continue
      }

      // Check if it's a function
      if (
        (member.type && ts.isFunctionTypeNode(member.type)) ||
        ts.isMethodSignature(member)
      ) {
        const daggerInterfaceFunction = new DaggerInterfaceFunction(
          member,
          this.ast,
        )
        this.functions[daggerInterfaceFunction.name] = daggerInterfaceFunction
        continue
      }

      // TODO(TomChv): Add support for fields
    }
  }

  public getReferences(): string[] {
    const references: string[] = []
    for (const fn of Object.values(this.functions)) {
      references.push(...fn.getReferences())
    }
    return references.filter((v, i, arr) => arr.indexOf(v) === i)
  }

  public propagateReferences(references: References): void {
    for (const fn of Object.values(this.functions)) {
      fn.propagateReferences(references)
    }
  }

  public toJSON() {
    return {
      name: this.name,
      description: this.description,
      functions: this.functions,
    }
  }
}
