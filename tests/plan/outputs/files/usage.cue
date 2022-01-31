package main

import "dagger.io/dagger"

dagger.#Plan & {
	outputs: files: {
		[path=string]: dest: path
		"test.sh": {
			contents: """
				#!/bin/bash
				set -euo pipefail
				echo "Hello World!"

				"""
			permissions: 0o750
		}
	}
}
