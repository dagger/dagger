import ts from "typescript"

import { IntrospectionError } from "../../../common/errors/index.js"
import { AST } from "../typescript_module/index.js"
import { DaggerEnumBase, DaggerEnumBaseValue } from "./enumBase.js"
import { Locatable } from "./locatable.js"

export type DaggerEnumClasses = { [name: string]: DaggerEnumClass }

export type DaggerEnumClassValues = { [name: string]: DaggerEnumClassValue }

export class DaggerEnumClassValue
  extends Locatable
  implements DaggerEnumBaseValue
{
  public name: string
  public value: string
  public description: string
  public deprecated?: string

  private symbol: ts.Symbol

  constructor(
    private readonly node: ts.PropertyDeclaration,
    private readonly ast: AST,
  ) {
    super(node)
    this.name = this.node.name.getText()
    this.symbol = this.ast.getSymbolOrThrow(this.node.name)
    const { description, deprecated } = this.ast.getSymbolDoc(this.symbol)
    this.description = description
    this.deprecated = deprecated

    const initializer = this.node.initializer
    if (!initializer) {
      throw new Error("Dagger enum value has no value set")
    }

    this.value = this.ast.resolveParameterDefaultValue(initializer)
  }

  toJSON() {
    return {
      name: this.name,
      value: this.value,
      description: this.description,
      deprecated: this.deprecated,
    }
  }
}

export class DaggerEnumClass extends Locatable implements DaggerEnumBase {
  public name: string
  public description: string
  public values: DaggerEnumClassValues = {}

  private symbol: ts.Symbol

  constructor(
    private readonly node: ts.ClassDeclaration,
    private readonly ast: AST,
  ) {
    super(node)

    if (!this.node.name) {
      throw new IntrospectionError(
        `could not resolve name of enum at ${AST.getNodePosition(node)}.`,
      )
    }
    this.name = this.node.name.getText()
    this.symbol = this.ast.getSymbolOrThrow(this.node.name)
    this.description = this.ast.getDocFromSymbol(this.symbol)

    const properties = this.node.members
    for (const property of properties) {
      if (ts.isPropertyDeclaration(property)) {
        const value = new DaggerEnumClassValue(property, this.ast)

        this.values[value.name] = value
      }
    }
  }

  toJSON() {
    return {
      name: this.name,
      description: this.description,
      values: this.values,
    }
  }
}
