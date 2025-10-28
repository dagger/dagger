/* eslint-disable @typescript-eslint/no-explicit-any */
import ts from "typescript"

import { TypeDefKind } from "../../../api/client.gen.js"
import { IntrospectionError } from "../../../common/errors/index.js"
import { ArgumentOptions } from "../../registry.js"
import { TypeDef } from "../typedef.js"
import {
  AST,
  isTypeDefResolved,
  resolveTypeDef,
} from "../typescript_module/index.js"
import { ARGUMENT_DECORATOR } from "./decorator.js"
import { Locatable } from "./locatable.js"
import { References } from "./reference.js"

export type DaggerArguments = { [name: string]: DaggerArgument }

export class DaggerArgument extends Locatable {
  public name: string
  public description: string
  public deprecated?: string
  private _typeRef?: string
  public type?: TypeDef<TypeDefKind>
  public isVariadic: boolean
  public isNullable: boolean
  public isOptional: boolean
  public defaultPath?: string
  public ignore?: string[]
  public defaultValue?: any

  private symbol: ts.Symbol

  constructor(
    private readonly node: ts.ParameterDeclaration,
    private readonly ast: AST,
  ) {
    super(node)

    this.symbol = this.ast.getSymbolOrThrow(node.name)
    this.name = this.node.name.getText()
    const { description, deprecated } = this.ast.getSymbolDoc(this.symbol)
    this.description = description
    this.deprecated = deprecated
    this.defaultValue = this.getDefaultValue()
    this.isVariadic = this.node.dotDotDotToken !== undefined
    this.isNullable = this.getIsNullable()
    this.isOptional =
      this.isVariadic || // if argument has ...
      (this.defaultValue === undefined && // if argument has a default value that couldn't be resolved.
        this.node.initializer !== undefined) ||
      this.isNullable || // if argument is nullable
      this.node.questionToken !== undefined // if argument has ?

    if (this.deprecated !== undefined && !this.isOptional) {
      throw new IntrospectionError(
        `argument ${this.name} is required and cannot be deprecated at ${AST.getNodePosition(this.node)}.`,
      )
    }

    const decoratorArguments = this.ast.getDecoratorArgument<ArgumentOptions>(
      this.node,
      ARGUMENT_DECORATOR,
      "object",
    )

    if (decoratorArguments) {
      this.ignore = decoratorArguments.ignore
      this.defaultPath = decoratorArguments.defaultPath
    }

    this.type = this.getType()
  }

  /**
   * Get the type of the parameter.
   *
   * If for it's a complex type that cannot be
   * resolve yet, we save its string representation for further reference.
   */
  private getType(): TypeDef<TypeDefKind> | undefined {
    const type = this.ast.checker.getTypeAtLocation(this.node)

    const typedef = this.ast.tsTypeToTypeDef(this.node, type)
    if (typedef === undefined || !isTypeDefResolved(typedef)) {
      this._typeRef = this.ast.typeToStringType(type)
    }

    return typedef
  }

  private getIsNullable(): boolean {
    if (!this.node.type) {
      return false
    }

    if (ts.isUnionTypeNode(this.node.type)) {
      for (const _type of this.node.type.types) {
        if (_type.getText() === "null") {
          return true
        }
      }
    }

    return false
  }

  private getDefaultValue(): any {
    const initializer = this.node.initializer
    if (!initializer) {
      return undefined
    }

    return this.ast.resolveParameterDefaultValue(initializer)
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

  public propagateReferences(references: References) {
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

  toJSON() {
    return {
      name: this.name,
      description: this.description,
      deprecated: this.deprecated,
      type: this.type,
      isVariadic: this.isVariadic,
      isNullable: this.isNullable,
      isOptional: this.isOptional,
      defaultValue: this.defaultValue,
      defaultPath: this.defaultPath,
      ignore: this.ignore,
    }
  }
}
