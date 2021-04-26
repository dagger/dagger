package main

import "strings"
// import "dagger.io/dagger"
import "foo.io/bar"

// shh: dagger.#Secret

a: bar.#A
b: bar.B
c: bar.C

#Ex: {
	a: string
	b: string
}

ex1: #Ex
ex2: #Ex & { a: "a" }
ex3: #Ex.a

const: bar.CONST


foo: "strings": [...string]
// bar: strings.Join(["bar", "baz"], " ")

myString: strings.MaxRunes(5)
