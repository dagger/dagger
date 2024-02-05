import { func, object, field } from '../../../decorators/decorators.js'

@object("Boo")
export class Bar {
    @field("bar")
    baz: string = "baz"

    @field("oof")
    foo: number = 4

    constructor(baz: string = "baz", foo: number = 4) {
        this.baz = baz
        this.foo = foo
    }

    @func("zoo")
    za(): string {
        return this.baz
    }
}

@object('Foo')
export class HelloWorld {
    @func('testBar')
    bar(): Bar {
        return new Bar()
    }

    @func("bar")
    customBar(baz: string, foo: number): Bar {
        return new Bar(baz, foo)
    }

    @func('greet')
    helloWorld(name: string): string {
        return `hello ${name}`
    }
}