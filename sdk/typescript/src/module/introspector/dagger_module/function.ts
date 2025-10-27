import ts from "typescript"

import { TypeDefKind } from "../../../api/client.gen.js"
import { IntrospectionError } from "../../../common/errors/index.js"
import { FunctionOptions } from "../../registry.js"
import { TypeDef } from "../typedef.js"
import {
  AST,
  isTypeDefResolved,
  resolveTypeDef,
} from "../typescript_module/index.js"
import { DaggerArgument, DaggerArguments } from "./argument.js"
import { FUNCTION_DECORATOR } from "./decorator.js"
import { Locatable } from "./locatable.js"
import { References } from "./reference.js"

export type DaggerFunctions = { [name: string]: DaggerFunction }

export class DaggerFunction extends Locatable {
  public name: string
  public description: string
  private _returnTypeRef?: string
  public returnType?: TypeDef<TypeDefKind>
  public arguments: DaggerArguments = {}
  public alias: string | undefined
  public cache: string | undefined

  private signature: ts.Signature
  private symbol: ts.Symbol

  constructor(
    private readonly node: ts.MethodDeclaration,
    private readonly ast: AST,
  ) {
    super(node)

    this.symbol = this.ast.getSymbolOrThrow(node.name)
    this.signature = this.ast.getSignatureFromFunctionOrThrow(node)
    this.name = this.node.name.getText()
    this.description = this.ast.getDocFromSymbol(this.symbol)

    const functionArguments = this.ast.getDecoratorArgument<
      FunctionOptions | string
    >(this.node, FUNCTION_DECORATOR, "object")
    if (functionArguments) {
      if (typeof functionArguments === "string") {
        // previously only a single arg was accepted for alias, so if there's just
        // a string we intrepret it that way for backward compatibility
        this.alias = functionArguments
      } else {
        this.alias = functionArguments.alias
        this.cache = functionArguments.cache
      }
    }

    for (const parameter of this.node.parameters) {
      this.arguments[parameter.name.getText()] = new DaggerArgument(
        parameter,
        this.ast,
      )
    }
    this.returnType = this.getReturnType()
  }

  private getReturnType(): TypeDef<TypeDefKind> | undefined {
    const type = this.signature.getReturnType()

    const typedef = this.ast.tsTypeToTypeDef(this.node, type)
    if (typedef === undefined || !isTypeDefResolved(typedef)) {
      this._returnTypeRef = this.ast.typeToStringType(type)
    }

    return typedef
  }

  public getArgsOrder(): string[] {
    return Object.keys(this.arguments)
  }

  public getReferences(): string[] {
    const references: string[] = []

    if (
      this._returnTypeRef &&
      (this.returnType === undefined || !isTypeDefResolved(this.returnType))
    ) {
      references.push(this._returnTypeRef)
    }

    for (const argument of Object.values(this.arguments)) {
      const reference = argument.getReference()
      if (reference) {
        references.push(reference)
      }
    }

    return references
  }

  public propagateReferences(references: References) {
    for (const argument of Object.values(this.arguments)) {
      argument.propagateReferences(references)
    }

    if (!this._returnTypeRef) {
      return
    }

    if (this.returnType && isTypeDefResolved(this.returnType)) {
      return
    }

    const typeDef = references[this._returnTypeRef]
    if (!typeDef) {
      throw new IntrospectionError(
        `could not find type reference for ${this._returnTypeRef} at ${AST.getNodePosition(this.node)}.`,
      )
    }

    this.returnType = resolveTypeDef(this.returnType, typeDef)
  }

  public toJSON() {
    return {
      name: this.name,
      description: this.description,
      alias: this.alias,
      arguments: this.arguments,
      returnType: this.returnType,
    }
  }
}
