package main

import (
	"crypto/rand"
)

type Cred struct {
	Token string
}

// Login returns a credential with a per-call token. Never cached, like a
// real credential fetcher, so the result is only owned by the sessions that
// hold it.
// +cache="never"
func (c *Cred) Login() *Cred {
	return &Cred{Token: rand.Text()}
}

// Show returns the current token without caching the call.
// +cache="never"
func (c *Cred) Show() string {
	return c.Token
}
