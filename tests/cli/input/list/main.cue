package main

import (
	"dagger.io/dagger"
	"dagger.io/aws"
)

awsConfig: aws.#Config & {
	// force region
	region: "us-east-1"
}

#A: {
	// source dir
	source:         dagger.#Artifact @dagger(input)
	sourceNotInput: dagger.#Artifact

	// a secret
	key:         dagger.#Secret @dagger(input)
	keyNotInput: dagger.#Secret

	// a string
	str:    string           @dagger(input)
	strSet: "pipo"           @dagger(input)
	strDef: *"yolo" | string @dagger(input)

	// a number
	num:         int | *42 @dagger(input)
	numNotInput: int

	// aws config
	cfg: awsConfig
}

cfgInline: {
	#A
}

cfg: #A & {
	// force this key
	num: 21
}

cfg2: cfg
