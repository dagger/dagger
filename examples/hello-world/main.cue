package main

name:    string | *"world"
message: "Hello, \(name)!"


namespace: string @dagger(input)
labels: _ @dagger(input)
labels: [string]: string @dagger(input)

#Other: moo: string @dagger(input)

foo: {
	foo: string @dagger(input)
	bar?: string @dagger(input)

	cows: [string]: #Other

}
