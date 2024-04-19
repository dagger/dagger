// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

// package exported contains internal types that are re-exported from a public package
package exported

// AssertionRequestOptions has information required to generate a client assertion
type AssertionRequestOptions struct {
	// ClientID identifies the application for which an assertion is requested. Used as the assertion's "iss" and "sub" claims.
	ClientID string

	// TokenEndpoint is the intended token endpoint. Used as the assertion's "aud" claim.
	TokenEndpoint string
}
