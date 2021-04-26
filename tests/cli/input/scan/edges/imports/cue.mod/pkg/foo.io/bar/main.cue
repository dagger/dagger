package bar

CONST: "const-value"

#A: {
	a: string
}

B: string

C: {
	A: #A
	c: A.a
	d: #A.a
}
