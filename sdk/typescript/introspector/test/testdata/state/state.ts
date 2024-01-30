/**
 * An Alpine Module for testing purpose only.
 * 
 * Warning: Do not reproduce in production.
 */
import { func, object, field } from '../../../decorators/decorators.js'
import { dag, Container } from '../../../../api/client.gen.js'


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
    @field
    public packages: string[] = []

    @field
    ctr?: Container = undefined

    /**
     * Returns a base Alpine container
     * @param version version to use (default to: 3.16.2)
     */
    @func
    base(version?: string): Alpine {
        if (version) {
            this.version = version
        }

        this.ctr = dag.container().from(`alpine:${this.version}`)

        return this
    }

    @func
    install(pkgs: string[]): Alpine {
        this.packages.push(...pkgs)

        return this
    }

    @func
    async exec(cmd: string[]): Promise<string> {
        return this
            .ctr!
            .withExec(["apk", "add", ...this.packages])
            .withExec(cmd)
            .withUser(this.user)
            .stdout()
    }
}