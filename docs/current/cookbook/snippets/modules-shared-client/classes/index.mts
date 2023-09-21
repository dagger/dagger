import { connect, Client } from "@dagger.io/dagger"

import { Pipelines } from "./pipelines.mts"

connect(
	// initialize Dagger client
	// pass client to method imported from another module
	async (client: Client) => {
		const pipeline = new Pipelines(client)

		console.log(await pipeline.version())
	},
	{ LogOutput: process.stderr }
)