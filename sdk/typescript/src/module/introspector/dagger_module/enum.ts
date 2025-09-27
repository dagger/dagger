import ts from "typescript"

import { IntrospectionError } from "../../../common/errors/index.js"
import { AST } from "../typescript_module/index.js"
import { DaggerEnumBase, DaggerEnumBaseValue } from "./enumBase.js"
import { Locatable } from "./locatable.js"

export type DaggerEnums = { [name: string]: DaggerEnum }
export type DaggerEnumValues = { [name: string]: DaggerEnumValue }

export class DaggerEnumValue extends Locatable implements DaggerEnumBaseValue {
  public name: string
  public value: string
  public description: string
  public deprecated?: string

  private symbol: ts.Symbol

  constructor(
    private readonly node: ts.EnumMember,
    private readonly ast: AST,
  ) {
    super(node)
    this.symbol = this.ast.getSymbolOrThrow(this.node.name)
    this.name = this.node.name.getText()
    const { description, deprecated } = this.ast.getSymbolDoc(this.symbol)
    this.description = description
    this.deprecated = deprecated

    const initializer = this.node.initializer
    if (!initializer) {
      throw new IntrospectionError(
        `enum ${this.name} at ${AST.getNodePosition(this.node)} has no value set to its member.`,
      )
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

export class DaggerEnum extends Locatable implements DaggerEnumBase {
  public name: string
  public description: string
  public values: DaggerEnumValues = {}

  private symbol: ts.Symbol

  constructor(
    private readonly node: ts.EnumDeclaration,
    private readonly ast: AST,
  ) {
    super(node)

    this.name = this.node.name.getText()
    this.symbol = this.ast.getSymbolOrThrow(this.node.name)
    this.description = this.ast.getDocFromSymbol(this.symbol)

    const members = this.node.members
    for (const member of members) {
      const value = new DaggerEnumValue(member, this.ast)

      this.values[value.name] = value
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
