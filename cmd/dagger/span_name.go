package main

import (
	"strings"
)

// this diastrous hack sanitizes the commandline so dagger call invocations get
// a shorter name for the trace
//
// constructor flags are skipped, since they tend to not be worth their length
// and not unique to the command.
//
// flags passed later the chain are kept, since they're sometimes the only
// difference.
//
// if the command is not a call, the original command is kept in whole.
//
// eventually this should be replaced with browsing by function, rather than
// browsing by CLI.
//
// NOTE: this will get confused by boolean flags. there's nothing we can do
// about that. this should eventually be replaced with browse-by-function.
func spanName(args []string) string {
	keep := []string{}
	fullCall := []string{}
	var seenCommand bool
	var isCall bool
	var isFlag bool
	var keepFlag bool
	var pastConstructor bool
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			// we're a flag
			isFlag = true

			if strings.Contains(arg, "=") {
				// this flag is self-contained
				isFlag = false
			}

			if seenCommand {
				// we're a flag to a command
				if isCall {
					if pastConstructor {
						// we're a flag passed to a function; keep
						keep = append(keep, arg)
						keepFlag = true
					} else {
						// we're a flag to the constructor; skip, since these tend to be verbose
						// and not unique
						continue
					}
				} else {
					// we're a flag to a random command; keep
					keep = append(keep, arg)
				}
			} else {
				// we're a flag preceding any command (maybe --debug); drop
				continue
			}
			continue
		}

		if isFlag {
			// we're a flag value
			isFlag = false
			if keepFlag {
				// keep the flag value
				keep = append(keep, arg)
				keepFlag = false
			}
			continue
		}

		// we're not a flag, so we must be a command
		seenCommand = true

		if isCall {
			// we're a function in a call chain; keep
			keep = append(keep, arg)
			// from here on flags go to a function, not the constructor, whose flags are skipped
			pastConstructor = true
			continue
		}

		if len(keep) == 0 && arg == "call" {
			// we're the call command; exclude 'call' and parse the remainder as the chain
			isCall = true
			fullCall = args[i+1:]
			continue
		}
	}
	if !isCall {
		// if we're not a call, just use the original command
		keep = args
	} else if len(keep) == 0 {
		// we're a call, but failed to parse the chain, probably confused by a
		// boolean flag, so just show the full call
		keep = fullCall
	}
	return strings.Join(keep, " ")
}
