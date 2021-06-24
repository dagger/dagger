package os

import (
	"dagger.io/dagger"
)

// Test secret mount
SimpleSecret: {
	// 'encrypted' and 'cleartext' must be set from identical values
	encrypted: dagger.#Secret @dagger(input)
	cleartext: string         @dagger(input)

	ctr: #Container & {
		secret: "/secret-in": encrypted
		command: "cat /secret-in > /secret-out"
	}

	// Decrypted secret
	decrypted: (#File & {
			from: ctr
			path: "/secret-out"
	}).contents @dagger(output)

	// Assertion: decrypted value must match original cleartext
	decrypted: cleartext
}
