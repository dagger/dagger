import ts from "typescript"

import { Constructor } from "./constructor.js"
import { UnknownDaggerError } from "../../../common/errors/UnknownDaggerError.js"
import { Method } from "./method.js"
import { Property } from "./property.js"
import { ClassTypeDef, FieldTypeDef, FunctionTypedef } from "../typeDefs.js"
import { isFunction } from "../utils.js"

export class Object {
  private checker: ts.TypeChecker

  private class: ts.ClassDeclaration

  private symbol: ts.Symbol

  /**
   *
   * @param checker The checker to use to introspect the class.
   * @param classDeclaration The class to introspect.
   *
   * @throws UnknownDaggerError If the class doesn't have a name.
   * @throws UnknownDaggerError If the class doesn't have a symbol.
   */
  constructor(checker: ts.TypeChecker, classDeclaration: ts.ClassDeclaration) {
    this.checker = checker
    this.class = classDeclaration

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

  get methods(): Method[] {
    return this.class.members
      .filter((member) => ts.isMethodDeclaration(member) && isFunction(member))
      .map((member) => new Method(this.checker, member as ts.MethodDeclaration))
  }

  get properties(): Property[] {
    return this.class.members
      .filter((member) => ts.isPropertyDeclaration(member))
      .map(
        (member) =>
          new Property(this.checker, member as ts.PropertyDeclaration),
      )
  }

  // TODO(TomChv): replace with `ToJson` method
  // after the refactor is complete.
  get typeDef(): ClassTypeDef {
    return {
      name: this.name,
      description: this.description,
      constructor: this._constructor?.typeDef,
      methods: this.methods.reduce(
        (acc: { [name: string]: FunctionTypedef }, method) => {
          acc[method.alias ?? method.name] = method.typeDef
          return acc
        },
        {},
      ),
      fields: this.properties.reduce(
        (acc: { [name: string]: FieldTypeDef }, property) => {
          acc[property.alias ?? property.name] = property.typeDef
          return acc
        },
        {},
      ),
    }
  }
}
