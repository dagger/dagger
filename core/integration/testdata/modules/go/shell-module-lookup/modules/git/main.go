// A git helper
package main

func New(url string) *Git {
	return &Git{URL: url}
}

type Git struct {
	URL string
}
