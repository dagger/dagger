package main

foo: string

name:    string | *"world"
message: "Hello, \(name)!"

optional?: string

pattern: [string]: string

bar: {
	a: string
	b: int
}

list: [...string]

