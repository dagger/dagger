import { fct, object, Container, dag } from '@dagger.io/dagger'

/**
 * Alpine module
 */
@object
export class Alpine {
    private version = "3.16.2"

    protected user = "root"

    /**
     * packages to install
     */
    public packages: string[]

    ctr: Container

    /**
     * Returns a base Alpine container
     * @param version version to use (default to: 3.16.2)
     */
    @fct
    base(version?: string): Alpine {
        if (version === undefined) {
            version = this.version
        }

        this.ctr = dag.container().from(`alpine:${version}`)

        return this
    }

    @fct
    install(pkgs: string[]): Alpine {
        this.packages.push(...pkgs)

        return this
    }

    @fct
    async exec(cmd: string[]): Promise<string> {
        return this
            .ctr
            .withExec(["apk", "add", ...this.packages])
            .withExec(cmd)
            .withUser(this.user)
            .stdout()
    }
}