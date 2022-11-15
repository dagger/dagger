import assert from 'assert';
import { getProvisioner } from '../provisioner.js';
import { DEFAULT_HOST } from '../default.js';

describe('Provisioner', function () {
	describe('Docker Image', function () {
		it('Should create a GQL client on call to connect', async function () {
			this.timeout(30000);
			const provisioner = getProvisioner(DEFAULT_HOST);

			after(async function () {
				await provisioner.Close();
			});

			const client = await provisioner.Connect({});
			assert.notEqual(client, undefined);
		});
	});
});