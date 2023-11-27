import { fct, object, dag } from '@dagger.io/dagger'

/**
 * Bar class
 */
@object
export class Bar {
    /**
     * Execute the command and return its result
     * @param cmd Command to execute
     */
    @fct
    async exec(cmd: string[]): Promise<string> {
        return await dag
            .container()
            .from("alpine")
            .withExec(cmd)
            .stdout()
    }
}