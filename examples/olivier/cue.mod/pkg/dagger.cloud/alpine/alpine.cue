package alpine

// Default version pinned to digest. Manually updated.
let defaultDigest="sha256:3c7497bf0c7af93428242d6176e8f7905f2201d8fc5861f45be7a346b5f23436"

ref: string 

// Match a combination of inputs 'version' and 'digest':
*{
	// no version, no digest:
	ref: "index.docker.io/alpine@\(defaultDigest)"
} | {
	// version, no digest
	version: string
	ref: "alpine:\(version)"
} | {
	// digest, no version
	digest: string
	ref: "alpine@\(digest)"
} | {
	// version and digest
	version: string
	digest: string
	ref: "alpine:\(version)@\(digest)"
}

// Packages to install
package: [string]: true | false | string

#dagger: compute: [
	{
		do: "fetch-container"
		"ref": ref
	},
	for pkg, info in package {
		if (info & true) != _|_ {
			do: "exec"
			args: ["apk", "add", "-U", "--no-cache", pkg]
		}
		if (info & string) != _|_  {
			do: "exec"
			args: ["apk", "add", "-U", "--no-cache", "\(pkg)\(info)"]
		}
	},
]

