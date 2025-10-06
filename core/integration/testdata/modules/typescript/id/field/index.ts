import { object, func } from "@dagger.io/dagger";

@object()
export class Test {
  @func()
  fn(): CustomObject {
    return new CustomObject("NOOOO!!!!");
  }
}

@object()
export class CustomObject {
  @func()
  ID: string;

  constructor(id: string) {
    this.ID = id;
  }
}
