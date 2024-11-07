import { TypeDefKind } from "../../../api/client.gen.js"
import { TypeDef } from "../typedef.js"
import { DaggerConstructor } from "./constructor.js"
import { DaggerFunctions } from "./function.js"
import { References } from "./reference.js"

export interface DaggerObjectPropertyBase {
  name: string
  description: string
  alias?: string
  isExposed: boolean
  type?: TypeDef<TypeDefKind>

  propagateReferences(references: References): void
}

export type DaggerObjectPropertiesBase = {
  [name: string]: DaggerObjectPropertyBase
}

export interface DaggerObjectBase {
  name: string
  description: string
  _constructor: DaggerConstructor | undefined
  methods: DaggerFunctions
  properties: DaggerObjectPropertiesBase

  kind(): "class" | "object"

  propagateReferences(references: References): void
}

export type DaggerObjectsBase = { [name: string]: DaggerObjectBase }
