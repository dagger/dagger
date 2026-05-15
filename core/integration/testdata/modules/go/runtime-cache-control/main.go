package main

import (
	"crypto/rand"
)

type Test struct{}

// My cool doc on TestTtl
// +cache="40s"
func (m *Test) TestTtl() string {
	return rand.Text()
}

// My dope doc on TestCachePerSession
// +cache="session"
func (m *Test) TestCachePerSession() string {
	return rand.Text()
}

// My darling doc on TestNeverCache
// +cache="never"
func (m *Test) TestNeverCache() string {
	return rand.Text()
}

// My rad doc on TestAlwaysCache
func (m *Test) TestAlwaysCache() string {
	return rand.Text()
}
