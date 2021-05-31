package main

#A: {
	// a string
	str:    string           @dagger(output)
	strSet: "pipo"           @dagger(input)
	strDef: *"yolo" | string @dagger(input)

	// test url description
	url: "http://this.is.a.test/" @dagger(output)
	url2: url
}

cfgInline: {
	#A
}

cfg: #A
cfg2: cfg
