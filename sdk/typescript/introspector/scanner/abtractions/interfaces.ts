import ts from "typescript"

import { UnknownDaggerError } from "../../../common/errors/UnknownDaggerError.js"
import { isClassAbstract, isMethodAbstract } from "../utils.js"
import { Method, Methods } from "./method.js"
import { Constructor } from "./constructor.js"
import { Properties } from "./property.js"
import { DaggerClass } from "./class.js"

export type DaggerInterfaces = { [name: string]: DaggerInterface }

export class DaggerInterface implements DaggerClass {
  private checker: ts.TypeChecker

  private class: ts.ClassDeclaration

  private symbol: ts.Symbol

  public file: ts.SourceFile

  /**
   *
   * @param checker The checker to use to introspect the class.
   * @param classDeclaration The class to introspect.
   *
   * @throws UnknownDaggerError If the class doesn't have a name.
   * @throws UnknownDaggerError If the class doesn't have a symbol.
   */
  constructor(
    checker: ts.TypeChecker,
    file: ts.SourceFile,
    classDeclaration: ts.ClassDeclaration,
  ) {
    this.checker = checker
    this.class = classDeclaration
    this.file = file

    if (!classDeclaration.name) {
      throw new UnknownDaggerError(
        `could not introspect class: ${classDeclaration}`,
        {},
      )
    }

    if (!isClassAbstract(classDeclaration)) {
      throw new UnknownDaggerError(
        `interface must be abstract: ${classDeclaration.name.getText()}`,
        {},
      )
    }

    const classSymbol = checker.getSymbolAtLocation(classDeclaration.name)
    if (!classSymbol) {
      throw new UnknownDaggerError(
        `could not get class symbol: ${classDeclaration.name.getText()}`,
        {},
      )
    }

    this.symbol = classSymbol
  }

  get name(): string {
    return this.symbol.getName()
  }

  get _constructor(): Constructor | undefined {
    return undefined
  }

  get properties(): Properties {
    return {}
  }

  get description(): string {
    return ts.displayPartsToString(
      this.symbol.getDocumentationComment(this.checker),
    )
  }

  get methods(): Methods {
    return this.class.members
      .filter(
        (member) => ts.isMethodDeclaration(member) && isMethodAbstract(member),
      )
      .reduce((acc: Methods, member) => {
        const method = new Method(this.checker, member as ts.MethodDeclaration)

        acc[method.name] = method

        return acc
      }, {})
  }

  toJSON() {
    return {
      name: this.name,
      description: this.description,
      methods: this.methods,
    }
  }
}
