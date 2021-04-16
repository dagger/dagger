package main

foo: string

name:    string | *"world"
message: "Hello, \(name)!"

optional?: string

missing: [string]: string @dagger(input)

pattern: [string]: string
pattern: _ @dagger(input)

bar: {
	a: string
	b: int @dagger(computed)
}

#def: {
	missing: *"" | string
}

let A = string

refd: {
	a: string
	b: {
		ref1: a
		ref2: A
	}
	#c: {
		C: string
	}
}

#fld1: string

#fld2: string @dagger(input)

exec: {
	cmd: string
	#up: [{ foo: string }]
}

list: [...string]

