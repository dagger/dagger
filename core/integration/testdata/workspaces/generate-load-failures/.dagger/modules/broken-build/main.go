package main

// BrokenBuild does not compile: it references a symbol that does not exist,
// standing in for a module whose source no longer matches its SDK's API.
type BrokenBuild struct{}

func (m *BrokenBuild) Hello() string {
	return intentionallyUndefinedSymbol()
}
