import {
  argument,
  func,
  object,
  enumType,
  field,
  check,
  generate,
  up,
  agent,
  collection,
  keys,
  get,
  delta,
} from "../../decorators.js"

export type DaggerDecorators =
  | "collection"
  | "keys"
  | "get"
  | "delta"
  | "object"
  | "func"
  | "check"
  | "generate"
  | "up"
  | "agent"
  | "argument"
  | "enumType"
  | "field"

export const OBJECT_DECORATOR = object.name as DaggerDecorators
export const COLLECTION_DECORATOR = collection.name as DaggerDecorators
export const KEYS_DECORATOR = keys.name as DaggerDecorators
export const GET_DECORATOR = get.name as DaggerDecorators
export const DELTA_DECORATOR = delta.name as DaggerDecorators
export const FUNCTION_DECORATOR = func.name as DaggerDecorators
export const CHECK_DECORATOR = check.name as DaggerDecorators
export const GENERATOR_DECORATOR = generate.name as DaggerDecorators
export const UP_DECORATOR = up.name as DaggerDecorators
export const AGENT_DECORATOR = agent.name as DaggerDecorators
export const FIELD_DECORATOR = field.name as DaggerDecorators
export const ARGUMENT_DECORATOR = argument.name as DaggerDecorators
export const ENUM_DECORATOR = enumType.name as DaggerDecorators
