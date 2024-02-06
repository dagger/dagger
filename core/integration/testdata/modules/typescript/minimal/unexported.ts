import { object, func } from "@dagger.io/dagger"

@object
class Foo {
    @func
    hello(name: string): Foo {
        return new Foo()
    }
}

@object
class Minimal {
    @func
    hello(name: string): string {
        return name
    } 
}

class Bar {
    @func
    hello(name: string): string {
        return name
    }
}
