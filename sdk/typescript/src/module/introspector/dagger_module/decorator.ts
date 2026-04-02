import {
  argument,
  collection,
  func,
  object,
  enumType,
  field,
  check,
  generate,
  get,
  keys,
} from "../../decorators.js"

export type DaggerDecorators =
  | "collection"
  | "object"
  | "func"
  | "get"
  | "check"
  | "generate"
  | "argument"
  | "enumType"
  | "field"
  | "keys"

export const COLLECTION_DECORATOR = collection.name as DaggerDecorators
export const OBJECT_DECORATOR = object.name as DaggerDecorators
export const FUNCTION_DECORATOR = func.name as DaggerDecorators
export const GET_DECORATOR = get.name as DaggerDecorators
export const CHECK_DECORATOR = check.name as DaggerDecorators
export const GENERATOR_DECORATOR = generate.name as DaggerDecorators
export const FIELD_DECORATOR = field.name as DaggerDecorators
export const KEYS_DECORATOR = keys.name as DaggerDecorators
export const ARGUMENT_DECORATOR = argument.name as DaggerDecorators
export const ENUM_DECORATOR = enumType.name as DaggerDecorators
