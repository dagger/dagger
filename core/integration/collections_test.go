package core

const goCollectionModuleSource = `package main

import "strings"

type Test struct{}

func (m *Test) Tests() *GoTests {
	return &GoTests{
		Keys: []string{"unit", "lint", "integration"},
	}
}

type GoTest struct {
	Name string ` + "`json:\"name\"`" + `
}

// +collection
type GoTests struct {
	// +keys
	Keys []string ` + "`json:\"keys\"`" + `
}

func (tests *GoTests) Get(name string) *GoTest {
	return &GoTest{Name: name}
}

func (tests *GoTests) Names() string {
	return strings.Join(tests.Keys, ",")
}
`

const collectionKeysOutput = "unit\nlint\nintegration\n"
const collectionSubsetKeysOutput = "unit\nintegration\n"
const collectionBatchOutput = "unit,integration"
const collectionClientOutput = "{\"keys\":[\"unit\",\"lint\",\"integration\"],\"list\":[\"unit\",\"lint\",\"integration\"],\"subset\":[\"unit\",\"integration\"],\"batch\":\"unit,integration\",\"get\":\"lint\"}\n"
