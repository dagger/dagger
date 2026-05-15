package main

import "strings"

type Test struct{}

func (m *Test) UpperOpt(
	a string, // +optional
) string {
	return strings.ToUpper(a)
}

func (m *Test) UpperReq(
	a string,
) string {
	return strings.ToUpper(a)
}
