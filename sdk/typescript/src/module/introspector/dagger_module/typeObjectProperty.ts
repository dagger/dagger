import ts from "typescript"

import { TypeDefKind } from "../../../api/client.gen.js"
import { IntrospectionError } from "../../../common/errors/index.js"
import { TypeDef } from "../typedef.js"
import {
  AST,
  isTypeDefResolved,
  resolveTypeDef,
} from "../typescript_module/index.js"
import { Locatable } from "./locatable.js"
import { DaggerObjectPropertyBase } from "./objectBase.js"
import { References } from "./reference.js"

export type DaggerObjectTypeProperties = {
  [name: string]: DaggerObjectTypeProperty
}

export class DaggerObjectTypeProperty
  extends Locatable
  implements DaggerObjectPropertyBase
{
  public name: string
  public description: string
  public deprecated?: string
  public alias = undefined
  public isExposed: boolean = true

  private _typeRef?: string
  public type?: TypeDef<TypeDefKind>

  constructor(
    private readonly node: ts.TypeAliasDeclaration,
    private readonly symbol: ts.Symbol,
    private readonly ast: AST,
  ) {
    super(node)

    this.name = symbol.name
    const { description, deprecated } = this.ast.getSymbolDoc(this.symbol)
    this.description = description
    this.deprecated = deprecated

    const type = this.ast.checker.getTypeOfSymbolAtLocation(
      this.symbol,
      this.node,
    )
    this.type = this.ast.tsTypeToTypeDef(this.node, type)
    if (this.type === undefined || !isTypeDefResolved(this.type)) {
      this._typeRef = this.ast.typeToStringType(type)
    }
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
        `could not find type reference for ${this._typeRef}.`,
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
