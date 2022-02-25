package main

import "dagger.io/dagger"

dagger.#Plan & {
	outputs: files: {
		[path=string]: dest: path
		test_relative: contents: """
			#!/bin/bash
			set -euo pipefail
			echo "Hello World!"

			"""
	}
}
