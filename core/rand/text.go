// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the Go repository's LICENSE file.
//
// This file is copied from Go 1.24's `math/rand` package.
// It is included here temporarily for compatibility with Go 1.23
// and will be removed once this project updates to Go 1.24.
//
// Source: https://cs.opensource.google/go/go/+/refs/tags/go1.24.1:src/crypto/rand/text.go

package rand

import "crypto/rand"

const base32alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// Text returns a cryptographically random string using the standard RFC 4648 base32 alphabet
// for use when a secret string, token, password, or other text is needed.
// The result contains at least 128 bits of randomness, enough to prevent brute force
// guessing attacks and to make the likelihood of collisions vanishingly small.
// A future version may return longer texts as needed to maintain those properties.
func Text() string {
	// ⌈log₃₂ 2¹²⁸⌉ = 26 chars
	src := make([]byte, 26)
	rand.Read(src)
	for i := range src {
		src[i] = base32alphabet[src[i]%32]
	}
	return string(src)
}
