package defaults

#Foo: {
	name: string | *{string, #ui: string | *"checkbox"} @dagger(input)
}

#Bar: #Foo & {
	name: {string, #ui: string | *"text"} @dagger(input)
}
#Baz: {
	name: {
		(#Foo.name)
		#ui: string | *"bazzel"
	} @dagger(input)
}

foo: #Foo & {
	name: "foo"
}

bar: #Bar & {
	name: "bar"
}

baz: #Baz & {
	name: "baz"
}

