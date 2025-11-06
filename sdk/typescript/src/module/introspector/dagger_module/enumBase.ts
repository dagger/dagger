import { Locatable } from "./locatable.js"

export interface DaggerEnumBaseValue extends Locatable {
  name: string
  value: string
  description: string
  deprecated?: string
}

export type DaggerEnumBaseValues = { [name: string]: DaggerEnumBaseValue }

export interface DaggerEnumBase extends Locatable {
  name: string
  description: string
  values: DaggerEnumBaseValues
}

export type DaggerEnumsBase = { [name: string]: DaggerEnumBase }
