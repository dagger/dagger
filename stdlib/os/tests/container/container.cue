package os

import (
	"alpha.dagger.io/dagger"
)

// Test secret mount
SimpleSecret: {
	// 'encrypted' and 'cleartext' must be set from identical values
	encrypted: dagger.#Secret & dagger.#Input
	cleartext: string & dagger.#Input

	ctr: #Container & {
		secret: "/secret-in": encrypted
		command: "cat /secret-in > /secret-out"
	}

	// Decrypted secret
	decrypted: (#File & {
		from: ctr
		path: "/secret-out"
	}).contents & dagger.#Output

	// Assertion: decrypted value must match original cleartext
	decrypted: cleartext
}
