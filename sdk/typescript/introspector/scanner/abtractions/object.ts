import ts from "typescript"

import { UnknownDaggerError } from "../../../common/errors/UnknownDaggerError.js"
import { ClassTypeDef, FieldTypeDef, FunctionTypedef } from "../typeDefs.js"
import { isFunction } from "../utils.js"
import { Constructor } from "./constructor.js"
import { Method, Methods } from "./method.js"
import { Properties, Property } from "./property.js"

export type DaggerObjects = { [name: string]: DaggerObject }

export class DaggerObject {
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

  get description(): string {
    return ts.displayPartsToString(
      this.symbol.getDocumentationComment(this.checker),
    )
  }

  get _constructor(): Constructor | undefined {
    const constructor = this.class.members.find((member) => {
      if (ts.isConstructorDeclaration(member)) {
        return true
      }
    })

    if (!constructor) {
      return undefined
    }

    return new Constructor(
      this.checker,
      constructor as ts.ConstructorDeclaration,
    )
  }

  get methods(): Methods {
    return this.class.members
      .filter((member) => ts.isMethodDeclaration(member) && isFunction(member))
      .reduce((acc: Methods, member) => {
        const method = new Method(this.checker, member as ts.MethodDeclaration)

        acc[method.alias ?? method.name] = method

        return acc
      }, {})
  }

  get properties(): Properties {
    return this.class.members
      .filter((member) => ts.isPropertyDeclaration(member))
      .reduce((acc: Properties, member) => {
        const property = new Property(
          this.checker,
          member as ts.PropertyDeclaration,
        )

        acc[property.alias ?? property.name] = property
        return acc
      }, {})
  }

  // TODO(TomChv): replace with `ToJson` method
  // after the refactor is complete.
  get typeDef(): ClassTypeDef {
    return {
      name: this.name,
      description: this.description,
      constructor: this._constructor?.typeDef,
      methods: Object.entries(this.methods).reduce(
        (acc: { [name: string]: FunctionTypedef }, [name, method]) => {
          acc[name] = method.typeDef
          return acc
        },
        {},
      ),
      fields: Object.entries(this.properties).reduce(
        (acc: { [name: string]: FieldTypeDef }, [name, property]) => {
          acc[name] = property.typeDef
          return acc
        },
        {},
      ),
    }
  }

  toJSON() {
    return {
      name: this.name,
      description: this.description,
      constructor: this._constructor,
      methods: this.methods,
      properties: this.properties,
    }
  }
}
