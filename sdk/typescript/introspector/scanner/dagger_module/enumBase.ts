export interface DaggerEnumBaseValue {
  name: string
  value: string
  description: string
}

export type DaggerEnumBaseValues = { [name: string]: DaggerEnumBaseValue }

export interface DaggerEnumBase {
  name: string
  description: string
  values: DaggerEnumBaseValues
}

export type DaggerEnumsBase = { [name: string]: DaggerEnumBase }
