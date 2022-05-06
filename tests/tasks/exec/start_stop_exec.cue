package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: commands: random: {
		name: "sh"
		args: ["-e", "-c", "dd if=/dev/urandom bs=16 count=1 status=none | base64"]
	}

	actions: {
		image: core.#Pull & {
			source: "alpine:3.15"
		}

		// test basic ordering of start exec -> sync exec -> stop exec
		basicTest: {
			start: core.#Start & {
				input: image.output
				args: [
					"sleep", "30",
				]
			}

			sleep: core.#Exec & {
				input: image.output
				args: [
					"sh", "-c",
					#"""
						echo taking a quick nap
						sleep 1
						"""#,
				]
				always: true
			}

			stop: core.#Stop & {
				input: start
				_dep:  sleep
			}

			// 137 means the process was still running and got SIGKILL
			verify: stop.exit & 137
		}

		// test all the various parameters that can be applied to an exec
		execParamsTest: {
			sharedCache: core.#CacheDir & {
				id:          "dagger-start-stop-test-\(client.commands.random.stdout)"
				concurrency: "shared"
			}

			foodir: core.#Mkdir & {
				input: dagger.#Scratch
				path:  "/foo"
			}

			secretFile: core.#WriteFile & {
				input:    dagger.#Scratch
				path:     "/secret"
				contents: "shhh"
			}
			secret: core.#NewSecret & {
				input: secretFile.output
				path:  "/secret"
			}

			// this sets up the cache to be writable by the non-root user
			// in the #Start below
			initCache: core.#Exec & {
				input: image.output
				mounts: cache: {
					dest:     "/cache"
					contents: sharedCache
				}
				args: [
					"chmod", "a+rwx", "/cache",
				]
			}

			startExec: core.#Start & {
				input: initCache.output
				mounts: {
					cache: {
						dest:     "/cache"
						contents: sharedCache
					}
					fs: {
						dest:     "/fs"
						contents: foodir.output
					}
					secretMnt: {
						dest:     "/secret"
						contents: secret.output
						// "guest" user is 405 in alpine image
						uid: 405
					}
					temp: {
						dest:     "/temp"
						contents: core.#TempDir
					}
				}
				env: TEST:          "hey"
				hosts: "unit.test": "192.0.2.1"
				user:    "guest"
				workdir: "/tmp"
				args: [
					"sh", "-e", "-c",
					#"""
						test -d /fs/foo

						test "$(cat /secret)" = "shhh"
						ls -l /secret | grep -- "-r--------"

						test "$(stat -f -c %T /temp)" = "tmpfs"

						grep -q "unit.test" /etc/hosts
						grep -q "192.0.2.1" /etc/hosts

						test "$(whoami)" = "guest"

						test "$(pwd)" = "/tmp"

						test "$TEST" = "hey"

						echo yo > /cache/yo
						"""#,
				]
			}

			// verify the started exec wrote to the cache mount and it was shared
			syncExec: core.#Exec & {
				input: image.output
				mounts: cache: {
					dest:     "/cache"
					contents: sharedCache
				}
				args: [
					"sh", "-x", "-e", "-c",
					#"""
						for i in `seq 1 20`; do test -f /cache/yo && break || sleep 1; done
						test "$(cat /cache/yo)" = yo
						sleep 5 # give the Start process time to exit cleanly before moving to stop below
						"""#,
				]
			}

			stop: core.#Stop & {
				input: startExec
				_dep:  syncExec
			}

			verify: stop.exit & 0
		}
	}
}
