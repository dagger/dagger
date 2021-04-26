package main

import "strings"
import "dagger.io/dagger"

port: string | int
Port: string & =~"[d]{4:5}" | int & >1024

MyStr: string & =~"[d]{4:5}"
MyInt: int & >1024
MyPort: MyStr | MyInt

a: "A"

foo: a | string

b: strings.MinRunes(5)
s: dagger.#Secret
