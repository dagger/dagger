// This is the source of truth for the DESIRED STATE of the dagger command-line user interface (UI)
//
// - The CLI implementation is written manually, using this document as a spec. If you spot
//   a discrepancy between the spec and implementation, please report it or even better,
//   submit a patch to the implementation.
//
// - To propose changes to the UI, submit a patch to this spec. Patches to the CLI implementation
//   which don't match the spec will be rejected.
//
// - It is OK to propose changes to the CLI spec before an implementation is ready. To speed up
//   development, we tolerate a lag between the spec and implementation. This may change in the
//   future once the project is more mature.

package cli

import (
	"text/tabwriter"
)

// Examples:
//
// cue eval --out text
// cue eval --out text -e '#Dagger.command.query.usage'
#Dagger.usage

#Dagger: #Command & {

	name:        "dagger"
	description: "A system for application delivery as code (ADC)"

	doc: """
		Every dagger operation happens inside a STACK: an isolated sandbox with its own state and execution environment.

		A stack is made of 3 layers:
			1. Base configuration (see `dagger base`)
			2. Input values and artifacts (see `dagger input`)
			3. Output values and artifacts (see `dagger output`)

		If a command does not specify a stack explicitly, dagger tries to auto-select by searching for stacks connected
		to the current directory, either as a base, input or base:
			- If no stack matches the search, the command fails
			- If exactly one stack matches, the command proceeds in that stack
			- If more than one stack matches, the user is prompted to select one of them
		"""

	flag: {
		"--workspace": {
			alt:         "-w"
			description: "Select a workspace"
			default:     "$HOME/.dagger"
		}
		"--stack": {
			alt:         "-s"
			description: "Select a stack"
			arg:         "NAME"
		}
		"--log-format": {
			arg:         "string"
			description: "Log format (json, pretty). Defaults to json if the terminal is not a tty"
		}
		"--log-level": {
			alt:         "-l"
			arg:         "string"
			description: "Log level"
			default:     "debug"
		}
	}

	command: {
		new: {
			description: "Create a new stack"
			flag: {
				"--name": {
					alt:         "-n"
					description: "Specify a stack name"
					default:     "name of current directory"
				}

				"--base-dir": description: "Load base configuration from a local directory"

				"--base-git": description: "Load base configuration from a git repository"

				"--base-package": description: "Load base configuration from a cue package"

				"--base-file": description: "Load base configuration from a cue or json file"

				"--up": {
					alt:         "-u"
					description: "Bring the stack online"
				}

				"--setup": {
					arg:         "no|yes|auto"
					description: "Specify whether to prompt user for initial setup"
				}
			}
		}

		list: description: "List available stacks"

		query: {
			arg:         "[EXPR] [flags]"
			description: "Query the contents of a stack"
			doc:
				"""
					EXPR may be any valid CUE expression. The expression is evaluated against the stack contents. The stack is not changed.
					Examples:

					  # Print all contents of the input and base layers (omit output)
					  $ dagger query -l input,base

					  # Print complete contents for a particular component
					  $ dagger query www.build

					  # Print the URL of a deployed service
					  $ dagger query api.url

					  # Export environment variables from a deployment
					  $ dagger query -f json api.environment

					"""

			flag: {

				// FIXME: confusing flag choice?
				// Use --revision or --change or --change-id instead?
				"--version": {
					alt:         "-v"
					description: "Query a specific version of the stack"
					default:     "latest"
				}

				"--format": {
					alt:         "-f"
					description: "Output format"
					arg:         "json|yaml|cue|text|env"
				}

				"--layer": {
					alt: "-l"
					description: """
						Comma-separated list of layers to query (any of "input", "base", "output")
						"""
					default: "all"
				}

			}
		}

		up: {
			description: "Bring a stack online using latest base and inputs"
			flag: "--no-cache": description: "Disable all run cache"
		}

		down: {
			description: "Take a stack offline (WARNING: may destroy infrastructure)"
			flag: "--no-cache": description: "Disable all run cache"
		}

		history: description: "List past changes to a stack"

		destroy: {
			description: "Destroy a stack"

			flag: "--force": {
				alt:         "-f"
				description: "Destroy environment state even if cleanup pipelines fail to complete (EXPERTS ONLY)"
			}
		}

		base: {
			description: "Manage a stack's base configuration"

			command: {
				package: {
					description: "Load base configuration from a cue package"
					arg:         "PKG"
					doc:
						"""
						Examples:
						  dagger base package dagger.io/templates/jamstack
						"""
				}

				dir: {
					description: "Load base configuration from a local directory"
					arg:         "PATH"
					doc:
						"""
						Examples:
						  dagger base dir ./infra/prod
						"""
				}

				git: {
					description: "Load base configuration from a git repository"
					arg:         "REMOTE REF [SUBDIR]"
					doc:
						"""
						Examples:
						  dagger base git https://github.com/dagger/dagger main examples/simple
						"""
				}

				file: {
					description: "Load base configuration from a cue file"
					arg:         "PATH|-"
					doc:
						"""
						Examples:
						  dagger base file ./base.cue
						  echo 'message: "hello, \(name)!", name: string | *"world"' | dagger base file -
						"""
				}
			}
		}

		input: {
			description: "Manage a stack's inputs"

			command: {
				// FIXME: details of individual input commands
				dir: {description: "Add a local directory as input artifact"}
				git: description:       "Add a git repository as input artifact"
				container: description: "Add a container image as input artifact"
				value: description:     "Add an input value"
				secret: description:    "Add an encrypted input secret"
			}
		}

		output: {
			description: "Manage a stack's outputs"
			// FIXME: bind output values or artifacts
			// to local dir or file
			// BONUS: bind a stack output to another stack's input?
		}

		login: description: "Login to Dagger Cloud"

		logout: description: "Logout from Dagger Cloud"
	}
}

#Command: {
	// Command name
	name: string

	description: string
	doc:         string | *""

	// Flags
	flag: [fl=string]: #Flag & {name: fl}
	flag: "--help": {
		alt:         "-h"
		description: "help for \(name)"
	}

	// Sub-commands
	command: [cmd=string]: #Command & {
		name: cmd
	}

	arg: string | *"[command]"

	usage: {
		"""
		\(description)
		\(doc)

		Usage:
		  \(name) \(arg)

		Available commands:
		\(tabwriter.Write(#commands))
		
		Flags:
		\(tabwriter.Write(#flags))
		"""

		#commands: [ for name, cmd in command {
			"""
			  \(name)\t\(cmd.description)
			"""
		}]
		#flags: [ for name, fl in flag {
			"""
			  \(name), \(fl.alt)\t\(fl.description)
			"""
		}]
	}
}

#Flag: {
	name:        string
	alt:         string | *""
	description: string
	default?:    string
	arg?:        string
	example?:    string
}
