package main

import (
	"encoding/json"

	"dagger.io/dagger"
	"dagger.io/os"

	"dagger.io/js/yarn"
	"dagger.io/git"
	"dagger.io/docker"

	"dagger.io/aws"
	"dagger.io/aws/s3"
)

// dagger repository
repository: dagger.#Artifact @dagger(input)

// docs version
version: string | *"devel" @dagger(input)

// if repository is checked out at a tag, use it as the version
tag: git.#CurrentBranch & {
	"repository": repository
}
if (tag.name & =~"^v") != _|_ {
	version: tag.name
}

// AWS credentials
awsConfig: aws.#Config @dagger(input)

// Lint the markdowns
lint: os.#Container & {
	image: docker.#Pull & {
		from: "tmknom/markdownlint:0.23.1"
	}

	command: "markdownlint ./docs"
	dir:     "/src"
	mount: "/src": from: repository
}

// Build the docs website
docs: yarn.#Package & {
	source:   repository
	cwd:      "./tools/gendocs"
	buildDir: "./tools/gendocs/public"
	args: ["--prefix-paths"]
	env: VERSION: version
}

// Upload to S3
website: s3.#Sync & {
	config: awsConfig
	source: docs.build
	delete: true
	target: "s3://docs.dagger.io/\(version)/"
}

// generate and upload a `tags.json` file for the navbar version selector
tags: git.#Tags & {
	"repository": repository
}
versions: [ for name in tags.tags {tag: name}, {
	tag: "devel"
}]

versionsObject: s3.#Put & {
	config:       awsConfig
	sourceInline: json.Marshal(versions)
	contentType:  "application/json"
	target:       "s3://docs.dagger.io/tags.json"
}

// if we're building a release, redirect the docs website to this page
if version != "devel" {
	redirect: s3.#Put & {
		config:       awsConfig
		sourceInline: #"""
            <!DOCTYPE html>
            <html>
            <head>
                <meta http-equiv="refresh" content="0; url=/\#(version)">
            </head>
            <body>
                Nothing to see here; <a href="/\#(version)">see the latest version of the docs</a>.
            </body>
            </html>
            """#
		contentType:  "text/html"
		target:       "s3://docs.dagger.io/index.html"
	}
}
