package defaults

#Foo: {
	name: {string, #ui: string | *"checkbox"} @dagger(input)
}

#Bar: #Foo & {
	name: {string, #ui: string | *"text"} @dagger(input)
}

foo: #Foo & {
	name: "foo"
}

bar: #Bar & {
	name: "bar"
}

