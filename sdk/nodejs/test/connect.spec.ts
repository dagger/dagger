import { connect } from '../connect.js';
import assert from 'assert';

describe('NodeJS sdk', function () {
	it('Connect to local engine and execute a simple query to make sure it does not fail', async function () {
		this.timeout(60000);

		await connect(async (client) => {
			const result = await client.container()
				.from({ address: 'alpine' })
				.exec({ args: [ 'apk', 'add', 'curl' ] })
				.exec({ args: [ 'curl', 'https://dagger.io/' ] })
				.stdout().size();

			assert.ok(result.size > 10000);
		});
	});
});