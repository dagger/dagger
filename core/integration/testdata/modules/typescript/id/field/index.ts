import { object, func, field } from "@dagger.io/dagger"

@object
class Test {
  @func
  fn(): CustomObject {
    return new CustomObject("NOOOO!!!!")
  }
}

@object
class CustomObject {
  @field
  ID: string

  constructor(id: string) {
    this.ID = id
  }
}