import ts from "typescript"

import { TypeDefKind } from "../../../api/client.gen.js"
import { IntrospectionError } from "../../../common/errors/index.js"
import { TypeDef } from "../typedef.js"
import {
  AST,
  isTypeDefResolved,
  resolveTypeDef,
} from "../typescript_module/index.js"
import { FIELD_DECORATOR, FUNCTION_DECORATOR } from "./decorator.js"
import { Locatable } from "./locatable.js"
import { DaggerObjectPropertyBase } from "./objectBase.js"
import { References } from "./reference.js"

export type DaggerProperties = { [name: string]: DaggerProperty }

export class DaggerProperty
  extends Locatable
  implements DaggerObjectPropertyBase
{
  public name: string
  public description: string
  public deprecated?: string
  public alias: string | undefined
  public isExposed: boolean

  private symbol: ts.Symbol
  private _typeRef?: string
  public type?: TypeDef<TypeDefKind>

  constructor(
    private readonly node: ts.PropertyDeclaration,
    private readonly ast: AST,
  ) {
    super(node)

    if (!this.node.name) {
      throw new IntrospectionError(
        `could not resolve name of class at ${AST.getNodePosition(node)}.`,
      )
    }
    this.symbol = this.ast.getSymbolOrThrow(this.node.name)
    this.name = this.node.name.getText()

    this.isExposed =
      this.ast.isNodeDecoratedWith(this.node, FUNCTION_DECORATOR) ||
      this.ast.isNodeDecoratedWith(this.node, FIELD_DECORATOR)

    const { description, deprecated } = this.ast.getSymbolDoc(this.symbol)
    this.description = description
    this.deprecated = deprecated
    this.alias = this.getAlias()
    this.type = this.getType()
  }

  private getAlias(): string | undefined {
    let alias = this.ast.getDecoratorArgument<string>(
      this.node,
      FUNCTION_DECORATOR,
      "string",
    )

    if (alias) {
      return JSON.parse(alias.replace(/'/g, '"'))
    }

    alias = this.ast.getDecoratorArgument<string>(
      this.node,
      FIELD_DECORATOR,
      "string",
    )

    if (alias) {
      return JSON.parse(alias.replace(/'/g, '"'))
    }
  }

  private getType(): TypeDef<TypeDefKind> | undefined {
    const type = this.ast.checker.getTypeAtLocation(this.node)

    const typedef = this.ast.tsTypeToTypeDef(this.node, type)
    if (typedef === undefined || !isTypeDefResolved(typedef)) {
      this._typeRef = this.ast.typeToStringType(type)
    }

    return typedef
  }

  public getReference(): string | undefined {
    if (
      this._typeRef &&
      (this.type === undefined || !isTypeDefResolved(this.type))
    ) {
      return this._typeRef
    }

    return undefined
  }

  public propagateReferences(references: References): void {
    if (!this._typeRef) {
      return
    }

    if (this.type && isTypeDefResolved(this.type)) {
      return
    }

    const typeDef = references[this._typeRef]
    if (!typeDef) {
      throw new IntrospectionError(
        `could not find type reference for ${this._typeRef} at ${AST.getNodePosition(this.node)}.`,
      )
    }

    this.type = resolveTypeDef(this.type, typeDef)
  }

  public toJSON() {
    return {
      name: this.name,
      description: this.description,
      deprecated: this.deprecated,
      alias: this.alias,
      type: this.type,
      isExposed: this.isExposed,
    }
  }
}
