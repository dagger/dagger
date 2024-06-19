import { object, func } from "@dagger.io/dagger";

@object()
class Test {
  @func()
  fn(): CustomObject {
    return new CustomObject("NOOOO!!!!");
  }
}

@object()
class CustomObject {
  @func()
  ID: string;

  constructor(id: string) {
    this.ID = id;
  }
}
