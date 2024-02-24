TODO
import { object, field } from '@dagger.io/dagger';

@object()
class Person {
  /**
   * The name of the person.
   */
  @field()
  name: string = 'anonymous';

  /**
   * The age of the person.
   */
  @field()
  age: number;

  constructor(age: number, name?: string) {
    this.name = name ?? this.name;
    this.age = age;
  }
}
