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

package spec

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
		Every dagger command is applied to a route. A route is a complete delivery environment with its own layout, inputs and outputs.

		The same application can be delivered in different configurations using different routes.
		For example, an application may have a "production" route, staging routes for QA, and experimental development routes.

		If a route is not specified, dagger auto-selects one as follows:
		  * If exactly one route is connected to the current directory, the command proceeds automatically.
		  * If more than one route matches, the user is prompted to select one.
		  * If no route matches, the command returns an error.
		"""

	flag: {
		"--route": {
			alt:         "-r"
			description: "Select a route"
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
			description: "Create a new route"
			flag: {
				"--name": {
					alt:         "-n"
					description: "Specify a route name"
					default:     "name of current directory"
				}

				"--layout-dir": description: "Load layout from a local directory"

				"--layout-git": description: "Load layout from a git repository"

				"--layout-package": description: "Load layout from a cue package"

				"--layout-file": description: "Load layout from a cue or json file"

				"--up": {
					alt:         "-u"
					description: "Bring the route online"
				}

				"--setup": {
					arg:         "no|yes|auto"
					description: "Specify whether to prompt user for initial setup"
				}
			}
		}

		list: description: "List available routes"

		query: {
			arg:         "[EXPR] [flags]"
			description: "Query the contents of a route"
			doc:
				"""
					EXPR may be any valid CUE expression. The expression is evaluated against the route contents. The route is not changed.
					Examples:

					  # Print all contents of the layout and input layers (omit output)
					  $ dagger query -l input,layout

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
					description: "Query a specific version of the route"
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
						Comma-separated list of layers to query (any of "input", "layout", "output")
						"""
					default: "all"
				}

			}
		}

		up: {
			description: "Bring a route online with latest layout and inputs"
			flag: "--no-cache": description: "Disable all run cache"
		}

		down: {
			description: "Take a route offline (WARNING: may destroy infrastructure)"
			flag: "--no-cache": description: "Disable all run cache"
		}

		history: description: "List past changes to a route"

		delete: {
			description: "Delete a route after taking it offline (WARNING: may destroy infrastructure)"
		}

		layout: {
			description: "Manage a route's layout"

			command: {
				package: {
					description: "Load layout from a cue package"
					arg:         "PKG"
					doc:
						"""
						Examples:
						  dagger layout package dagger.io/templates/jamstack
						"""
				}

				dir: {
					description: "Load layout from a local directory"
					arg:         "PATH"
					doc:
						"""
						Examples:
						  dagger layout dir ./infra/prod
						"""
				}

				git: {
					description: "Load layout from a git repository"
					arg:         "REMOTE REF [SUBDIR]"
					doc:
						"""
						Examples:
						  dagger layout git https://github.com/dagger/dagger main examples/simple
						"""
				}

				file: {
					description: "Load layout from a cue file"
					arg:         "PATH|-"
					doc:
						"""
						Examples:
						  dagger layout file ./layout.cue
						  echo 'message: "hello, \(name)!", name: string | *"world"' | dagger layout file -
						"""
				}
			}
		}

		input: {
			description: "Manage a route's inputs"

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
			description: "Manage a route's outputs"
			// FIXME: bind output values or artifacts
			// to local dir or file
			// BONUS: bind a route output to another route's input?
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
