package main

name:    string | *"world" @input()
message: "Hello, \(name)!"

foo: string
bar: string

if name == "world" {
	foo: string @input()
}
if name != "world" {
	bar: string @input()
}




#A: {
	s: string @input()
}
#B: {
	@input()
	s: string
}
#C: {
	s: string
} @input()

a1: #A
a2: { #A }
b1: #B
b2: { #B }
c1: #C
c2: { #C }

