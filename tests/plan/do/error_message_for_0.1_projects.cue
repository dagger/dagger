package main

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/aws/s3"
	"alpha.dagger.io/netlify"
	"alpha.dagger.io/os"
)

repo: dagger.#Artifact & dagger.#Input

hello: {

	dir: dagger.#Artifact & dagger.#Input

	ctr: os.#Container & {
		command: """
			ls -l /src > /tmp/out
			"""
		mount: "/src": from: dir
	}

	f: os.#File & {
		from: ctr
		path: "/tmp/out"
	}

	message: f.contents & dagger.#Output
}

// Website
web: {
	source: os.#Dir & {
		from: repo
		path: "web"
	}

	url: string & dagger.#Output

	// Where to host the website?
	provider: *"s3" | "netlify" & dagger.#Input

	// Deploy to AWS S3
	if provider == "s3" {
		url:    "\(bucket.url)index.html"
		bucket: s3.#Put & {
			contentType: "text/html"
			"source":    source
		}
	}

	// Deploy to Netlify
	if provider == "netlify" {
		url: site.url

		site: netlify.#Site & {
			contents: source
		}
	}
}
