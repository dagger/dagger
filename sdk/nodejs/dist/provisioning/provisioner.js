import { DockerImage } from './docker-provision/image.js';
const provisioners = {
    'docker-image': (u) => new DockerImage(u),
};
/**
 * getProvisioner returns a ready to use Engine connector.
 * This method parse the given host to find out which kind
 * of provisioner shall be returned.
 *
 * It supports the following provisioners:
 * - docker-image
 */
export function getProvisioner(host) {
    const url = new URL(host);
    // Use slice(0, -1) to remove the extra : from the protocol
    // For instance http: -> http
    const provisioner = provisioners[url.protocol.slice(0, -1)];
    if (!provisioner) {
        throw new Error(`invalid dagger host: ${host}.`);
    }
    return provisioner(url);
}
