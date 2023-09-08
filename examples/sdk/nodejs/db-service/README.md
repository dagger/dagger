# Database Service in a Pipeline

This example shows the usage of a postgres database used in the integration tests for an application. The pipeline uses a postgres container as a service alongside the testing pipeline.

To run, `node ./build.js`

This will start a dagger pipeline to run the tests in `test/integration.test.js`
