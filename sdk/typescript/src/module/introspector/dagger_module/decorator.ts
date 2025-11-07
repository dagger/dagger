import { argument, func, object, enumType, field, check } from "../../decorators.js"

export type DaggerDecorators =
  | "object"
  | "func"
  | "check"
  | "argument"
  | "enumType"
  | "field"

export const OBJECT_DECORATOR = object.name as DaggerDecorators
export const FUNCTION_DECORATOR = func.name as DaggerDecorators
export const CHECK_DECORATOR = check.name as DaggerDecorators
export const FIELD_DECORATOR = field.name as DaggerDecorators
export const ARGUMENT_DECORATOR = argument.name as DaggerDecorators
export const ENUM_DECORATOR = enumType.name as DaggerDecorators
