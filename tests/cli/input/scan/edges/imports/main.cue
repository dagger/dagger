package main

import "dagger.io/dagger"
import "foo.io/bar"

shh: dagger.#Secret

a: bar.#A
b: bar.B
c: bar.C

