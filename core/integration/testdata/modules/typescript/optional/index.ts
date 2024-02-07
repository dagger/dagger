import { Directory, object, func, field} from "@dagger.io/dagger"

@object()
class Minimal {
    @field()
    src?: Directory

    @field()
    name?: string

    @func()
    isEmpty(): boolean {
        if (this.src !== undefined) {
            throw new Error(`src should be undefined but is ${this.src}`)
        }

        if (this.name !== undefined) {
            throw new Error(`name should be undefined but is ${this.name}`)
        }

        return true
    }

    @func()
    foo(x?: string): string {
        if (x !== undefined) {
            throw new Error("uh oh")
        }

        return ""
    }
}
