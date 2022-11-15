import { EngineConn } from './engineconn.js';
/**
 * getProvisioner returns a ready to use Engine connector.
 * This method parse the given host to find out which kind
 * of provisioner shall be returned.
 *
 * It supports the following provisioners:
 * - docker-image
 */
export declare function getProvisioner(host: string): EngineConn;
//# sourceMappingURL=provisioner.d.ts.map