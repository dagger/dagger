import { object, func } from "@dagger.io/dagger"

export enum MyEnum {
  A = "MyEnumA",
  B = "MyEnumB",
}

@object()
export class Dep {
  @func()
  fieldDef: string

  @func()
  funcDef(arg1: string, arg2?: string): string {
    return ""
  }

  @func()
  async collect(enumValue: MyEnum): Promise<void> {}
}
