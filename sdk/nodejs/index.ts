import Client from "./api/client.gen.js";

export { gql } from "graphql-tag";
export { GraphQLClient } from "graphql-request";

export { connect, ConnectOpts } from './connect.js';
export { getProvisioner } from './provisioning/index.js';

export default Client