package main

foo: string

name:    string | *"world"
message: "Hello, \(name)!"

optional?: string

missing: [string]: string

bar: {
	a:  string
	#c: string
	b:  int @dagger(computed)
}

// may be missing
#inputs: {
	hello:   string
	missing: *"" | string
}

// substitute
let A = string
let B = bar.a

//let Ba = bar.a
//let Bb = bar.b
let D = "hello"

refd: {
	a: string
	b: {
		ref1: a
		ref2: A
		aa:   B
		bb:   D
	}
	#c: C: string
}

#fld1: string

exec: {
	cmd: string
	#up: [{foo: string}]
}

list: [...string]
