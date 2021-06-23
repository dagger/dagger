package main

import (
	"dagger.io/dagger"
	"dagger.io/os"
)

// Test secret mount
SimpleSecret: {
	// 'encrypted' and 'cleartext' must be set from identical values
	encrypted: dagger.#Secret @dagger(input)
	cleartext: string         @dagger(input)

	ctr: os.#Container & {
		secret: "/secret-in": encrypted
		command: "cat /secret-in > /secret-out"
	}

	// Decrypted secret
	decrypted: (os.#File & {
			from: ctr
			path: "/secret-out"
	}).contents @dagger(output)

	// Assertion: decrypted value must match original cleartext
	decrypted: cleartext
}
