package main

// Fixable does not compile as committed; the fixer generator rewrites this
// file, standing in for a run that regenerates what a module needs to build.
type Fixable struct{}

func (m *Fixable) Hello() string {
	return fixableUndefinedSymbol()
}
