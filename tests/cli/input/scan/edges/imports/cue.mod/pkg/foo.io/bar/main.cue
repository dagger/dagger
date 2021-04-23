package bar

#A: {
	a: string
}

B: string

C: {
	A: #A
	c: A.a
	d: #A.a
}
