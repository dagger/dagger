#!/usr/bin/env nu
# Dagger Nushell Runtime Entrypoint
#
# This script serves as the execution entrypoint for Nushell-based Dagger modules.
# It has two modes of operation:
#
# 1. Registration mode (--register flag):
#    - Scans the module source for exported functions
#    - Extracts function signatures (name, parameters, types, return type)
#    - Outputs function metadata as JSON for Dagger to register
#
# 2. Execution mode (no flag):
#    - Receives function call request via stdin (JSON)
#    - Loads the user module
#    - Invokes the requested function with provided arguments
#    - Returns the result serialized as JSON

# Parse command line arguments
def main [
    --register  # Enable registration mode for function discovery
] {
    # Debug: Check what we receive
    # For now, always try registration mode since that's what we've implemented
    # TODO: Understand Dagger's actual protocol

    # Check if we're in registration mode or execution mode
    # Registration happens with --register flag
    # Execution happens when there's a Dagger session available
    if $register {
        # Registration mode: discover and output functions
        register_functions
    } else if ("_EXPERIMENTAL_DAGGER_RUNNER_HOST" in $env) {
        # Execution mode: we have a Dagger session
        execute_function ""
    } else {
        # Unknown mode - output error
        error make {
            msg: "Unknown runtime mode"
            label: {
                text: "Neither --register flag nor Dagger session found"
            }
        }
    }
}

# Registration mode: discover functions from main.nu
def register_functions [] {
    # Try /src/main.nu (in container) or ./main.nu (local testing)
    let module_path = if ("/src/main.nu" | path exists) {
        "/src/main.nu"
    } else if ("./main.nu" | path exists) {
        "./main.nu"
    } else if ("main.nu" | path exists) {
        "main.nu"
    } else {
        error make {
            msg: "Module file not found"
            label: {
                text: "Could not find main.nu in /src/, ./, or current directory"
            }
        }
        ""
    }

    # Read and parse the module file
    let source = open $module_path

    # Parse exported functions
    let functions = parse_exported_functions $source

    # Output functions as JSON to stdout (will be parsed by Go runtime)
    $functions | to json
}

# Parse Nushell source code to find exported functions
def parse_exported_functions [source: string] {
    # Split source into lines for parsing
    let lines = $source | lines

    # Find line indices where "export def" appears
    let export_lines = $lines
        | enumerate
        | where {|line| $line.item | str contains "export def"}
        | get index

    # For each export def, extract the full function definition
    let functions = $export_lines
        | each {|start_idx|
            parse_function_from_lines $lines $start_idx
        }
        | compact

    $functions
}

# Parse a complete function definition starting from export def line
def parse_function_from_lines [lines: list, start_idx: int] {
    # Get the export def line
    let first_line = $lines | get $start_idx

    # Extract function name from "export def name ["
    let name_match = $first_line | parse "export def {name} ["
    if ($name_match | is-empty) {
        return null
    }

    let func_name = $name_match.0.name | str trim

    # Find the closing bracket for parameters by collecting all lines
    # until we find "] ->" or "] {"
    let remaining_lines = $lines | skip ($start_idx + 1)

    mut params_lines = []
    mut return_type = "any"
    mut found_end = false

    # Also look for "# Returns: type" in comments above the function
    let comment_lines = $lines | skip (if $start_idx > 5 { $start_idx - 5 } else { 0 }) | take 5
    for line in $comment_lines {
        if ($line | str contains "# Returns:") {
            let return_match = $line | parse "# Returns: {type}"
            if not ($return_match | is-empty) {
                $return_type = $return_match.0.type | str trim
            }
        }
    }

    for line in $remaining_lines {
        if ($line | str contains "]") {
            # Found the closing bracket
            $params_lines = ($params_lines | append $line)

            # Check if there's a return type annotation (older style)
            if ($line | str contains "->") {
                # Extract return type from "] -> type {"
                let type_match = $line | parse "] -> {type}"
                if not ($type_match | is-empty) {
                    $return_type = $type_match.0.type | str trim | str replace " {" ""
                }
            }

            $found_end = true
            break
        } else {
            # Part of parameters
            $params_lines = ($params_lines | append $line)
        }
    }

    # Parse parameters from collected lines
    let params_text = $params_lines | str join "\n"
    let params = parse_parameters $params_text

    {
        name: $func_name
        parameters: $params
        return_type: $return_type
    }
}

# Parse function parameters from parameter string
def parse_parameters [params_str: string] {
    # Handle empty parameters
    if ($params_str | str trim | is-empty) {
        return []
    }

    # Remove the closing bracket and everything after (] -> type {)
    let clean_params = $params_str
        | lines
        | where {|line| not ($line | str contains "]")}
        | each {|line| $line | str trim}
        | where {|line| not ($line | is-empty)}

    # Parse each parameter line
    let params = $clean_params
        | each {|line|
            # Parse "name: type # comment" format
            let parts = $line | parse "{name}: {type}"
            if ($parts | is-empty) {
                null
            } else {
                let name = $parts.0.name | str trim
                let rest = $parts.0.type

                # Split by comment marker if present
                let type_parts = $rest | split row "#"
                let type = $type_parts.0 | str trim
                let description = if ($type_parts | length) > 1 {
                    $type_parts.1 | str trim
                } else {
                    ""
                }

                {name: $name, type: $type, description: $description}
            }
        }
        | compact

    $params
}

# Execution mode: invoke a specific function
def execute_function [stdin_data: string] {
    print -e "=== EXECUTION MODE STARTED ==="
    print -e $"stdin_data: ($stdin_data)"

    # Use dagger CLI to query the current function call
    let query = 'query { currentFunctionCall { name parentName inputArgs { name value } } }'

    print -e $"Running query: ($query)"

    let call_info = try {
        ^dagger query --doc $query | from json | get currentFunctionCall
    } catch { |err|
        print -e $"Error querying Dagger API: ($err)"
        error make {
            msg: "Failed to get function call context"
            label: {
                text: "Could not query Dagger API for current function call"
            }
        }
    }

    print -e $"call_info: ($call_info)"

    let func_name = $call_info.name
    let parent_name = $call_info.parentName
    let input_args = $call_info.inputArgs

    # Load the module functions
    use /src/main.nu *

    # Build arguments for the function call
    # Convert input_args array to a record
    mut args = {}
    for arg in $input_args {
        $args = ($args | insert $arg.name $arg.value)
    }

    # Call the function dynamically
    # Note: Nushell doesn't have great dynamic function calling, so we'll use a match
    let result = match $func_name {
        "hello" => (hello $args.name),
        "add" => (add $args.a $args.b),
        "echo" => (echo $args.message),
        _ => {
            error make {
                msg: $"Unknown function: ($func_name)"
                label: {
                    text: "Function not found in module"
                }
            }
        }
    }

    # Return the result using Dagger API
    let result_json = $result | to json
    let return_query = $'mutation { currentFunctionCall { returnValue\(value: "($result_json)"\) } }'

    ^dagger query --doc $return_query
}

# Entry point
# Script is invoked directly, no need to call main explicitly
