#
# bats-assert - Common assertions for Bats
#
# Written in 2016 by Zoltan Tombol <zoltan dot tombol at gmail dot com>
#
# To the extent possible under law, the author(s) have dedicated all
# copyright and related and neighboring rights to this software to the
# public domain worldwide. This software is distributed without any
# warranty.
#
# You should have received a copy of the CC0 Public Domain Dedication
# along with this software. If not, see
# <http://creativecommons.org/publicdomain/zero/1.0/>.
#

#
# assert.bash
# -----------
#
# Assertions are functions that perform a test and output relevant
# information on failure to help debugging. They return 1 on failure
# and 0 otherwise.
#
# All output is formatted for readability using the functions of
# `output.bash' and sent to the standard error.
#

# Fail and display the expression if it evaluates to false.
#
# NOTE: The expression must be a simple command. Compound commands, such
#       as `[[', can be used only when executed with `bash -c'.
#
# Globals:
#   none
# Arguments:
#   $1 - expression
# Returns:
#   0 - expression evaluates to TRUE
#   1 - otherwise
# Outputs:
#   STDERR - details, on failure
assert() {
  if ! "$@"; then
    batslib_print_kv_single 10 'expression' "$*" \
      | batslib_decorate 'assertion failed' \
      | fail
  fi
}

# Fail and display the expression if it evaluates to true.
#
# NOTE: The expression must be a simple command. Compound commands, such
#       as `[[', can be used only when executed with `bash -c'.
#
# Globals:
#   none
# Arguments:
#   $1 - expression
# Returns:
#   0 - expression evaluates to FALSE
#   1 - otherwise
# Outputs:
#   STDERR - details, on failure
refute() {
  if "$@"; then
    batslib_print_kv_single 10 'expression' "$*" \
      | batslib_decorate 'assertion succeeded, but it was expected to fail' \
      | fail
  fi
}

# Fail and display details if the expected and actual values do not
# equal. Details include both values.
#
# Globals:
#   none
# Arguments:
#   $1 - actual value
#   $2 - expected value
# Returns:
#   0 - values equal
#   1 - otherwise
# Outputs:
#   STDERR - details, on failure
assert_equal() {
  if [[ $1 != "$2" ]]; then
    batslib_print_kv_single_or_multi 8 \
        'expected' "$2" \
        'actual'   "$1" \
      | batslib_decorate 'values do not equal' \
      | fail
  fi
}

# Fail and display details if `$status' is not 0. Details include
# `$status' and `$output'.
#
# Globals:
#   status
#   output
# Arguments:
#   none
# Returns:
#   0 - `$status' is 0
#   1 - otherwise
# Outputs:
#   STDERR - details, on failure
assert_success() {
  if (( status != 0 )); then
    { local -ir width=6
      batslib_print_kv_single "$width" 'status' "$status"
      batslib_print_kv_single_or_multi "$width" 'output' "$output"
    } | batslib_decorate 'command failed' \
      | fail
  fi
}

# Fail and display details if `$status' is 0. Details include `$output'.
#
# Optionally, when the expected status is specified, fail when it does
# not equal `$status'. In this case, details include the expected and
# actual status, and `$output'.
#
# Globals:
#   status
#   output
# Arguments:
#   $1 - [opt] expected status
# Returns:
#   0 - `$status' is not 0, or
#       `$status' equals the expected status
#   1 - otherwise
# Outputs:
#   STDERR - details, on failure
assert_failure() {
  (( $# > 0 )) && local -r expected="$1"
  if (( status == 0 )); then
    batslib_print_kv_single_or_multi 6 'output' "$output" \
      | batslib_decorate 'command succeeded, but it was expected to fail' \
      | fail
  elif (( $# > 0 )) && (( status != expected )); then
    { local -ir width=8
      batslib_print_kv_single "$width" \
          'expected' "$expected" \
          'actual'   "$status"
      batslib_print_kv_single_or_multi "$width" \
          'output' "$output"
    } | batslib_decorate 'command failed as expected, but status differs' \
      | fail
  fi
}

# Fail and display details if `$output' does not match the expected
# output. The expected output can be specified either by the first
# parameter or on the standard input.
#
# By default, literal matching is performed. The assertion fails if the
# expected output does not equal `$output'. Details include both values.
#
# Option `--partial' enables partial matching. The assertion fails if
# the expected substring cannot be found in `$output'.
#
# Option `--regexp' enables regular expression matching. The assertion
# fails if the extended regular expression does not match `$output'. An
# invalid regular expression causes an error to be displayed.
#
# It is an error to use partial and regular expression matching
# simultaneously.
#
# Globals:
#   output
# Options:
#   -p, --partial - partial matching
#   -e, --regexp - extended regular expression matching
#   -, --stdin - read expected output from the standard input
# Arguments:
#   $1 - expected output
# Returns:
#   0 - expected matches the actual output
#   1 - otherwise
# Inputs:
#   STDIN - [=$1] expected output
# Outputs:
#   STDERR - details, on failure
#            error message, on error
assert_output() {
  local -i is_mode_partial=0
  local -i is_mode_regexp=0
  local -i is_mode_nonempty=0
  local -i use_stdin=0

  # Handle options.
  if (( $# == 0 )); then
    is_mode_nonempty=1
  fi

  while (( $# > 0 )); do
    case "$1" in
      -p|--partial) is_mode_partial=1; shift ;;
      -e|--regexp) is_mode_regexp=1; shift ;;
      -|--stdin) use_stdin=1; shift ;;
      --) shift; break ;;
      *) break ;;
    esac
  done

  if (( is_mode_partial )) && (( is_mode_regexp )); then
    echo "\`--partial' and \`--regexp' are mutually exclusive" \
      | batslib_decorate 'ERROR: assert_output' \
      | fail
    return $?
  fi

  # Arguments.
  local expected
  if (( use_stdin )); then
    expected="$(cat -)"
  else
    expected="$1"
  fi

  # Matching.
  if (( is_mode_nonempty )); then
    if [ -z "$output" ]; then
      echo 'expected non-empty output, but output was empty' \
        | batslib_decorate 'no output' \
        | fail
    fi
  elif (( is_mode_regexp )); then
    if [[ '' =~ $expected ]] || (( $? == 2 )); then
      echo "Invalid extended regular expression: \`$expected'" \
        | batslib_decorate 'ERROR: assert_output' \
        | fail
    elif ! [[ $output =~ $expected ]]; then
      batslib_print_kv_single_or_multi 6 \
          'regexp'  "$expected" \
          'output' "$output" \
        | batslib_decorate 'regular expression does not match output' \
        | fail
    fi
  elif (( is_mode_partial )); then
    if [[ $output != *"$expected"* ]]; then
      batslib_print_kv_single_or_multi 9 \
          'substring' "$expected" \
          'output'    "$output" \
        | batslib_decorate 'output does not contain substring' \
        | fail
    fi
  else
    if [[ $output != "$expected" ]]; then
      batslib_print_kv_single_or_multi 8 \
          'expected' "$expected" \
          'actual'   "$output" \
        | batslib_decorate 'output differs' \
        | fail
    fi
  fi
}

# Fail and display details if `$output' matches the unexpected output.
# The unexpected output can be specified either by the first parameter
# or on the standard input.
#
# By default, literal matching is performed. The assertion fails if the
# unexpected output equals `$output'. Details include `$output'.
#
# Option `--partial' enables partial matching. The assertion fails if
# the unexpected substring is found in `$output'. The unexpected
# substring is added to details.
#
# Option `--regexp' enables regular expression matching. The assertion
# fails if the extended regular expression does matches `$output'. The
# regular expression is added to details. An invalid regular expression
# causes an error to be displayed.
#
# It is an error to use partial and regular expression matching
# simultaneously.
#
# Globals:
#   output
# Options:
#   -p, --partial - partial matching
#   -e, --regexp - extended regular expression matching
#   -, --stdin - read unexpected output from the standard input
# Arguments:
#   $1 - unexpected output
# Returns:
#   0 - unexpected matches the actual output
#   1 - otherwise
# Inputs:
#   STDIN - [=$1] unexpected output
# Outputs:
#   STDERR - details, on failure
#            error message, on error
refute_output() {
  local -i is_mode_partial=0
  local -i is_mode_regexp=0
  local -i is_mode_empty=0
  local -i use_stdin=0

  # Handle options.
  if (( $# == 0 )); then
    is_mode_empty=1
  fi

  while (( $# > 0 )); do
    case "$1" in
      -p|--partial) is_mode_partial=1; shift ;;
      -e|--regexp) is_mode_regexp=1; shift ;;
      -|--stdin) use_stdin=1; shift ;;
      --) shift; break ;;
      *) break ;;
    esac
  done

  if (( is_mode_partial )) && (( is_mode_regexp )); then
    echo "\`--partial' and \`--regexp' are mutually exclusive" \
      | batslib_decorate 'ERROR: refute_output' \
      | fail
    return $?
  fi

  # Arguments.
  local unexpected
  if (( use_stdin )); then
    unexpected="$(cat -)"
  else
    unexpected="$1"
  fi

  if (( is_mode_regexp == 1 )) && [[ '' =~ $unexpected ]] || (( $? == 2 )); then
    echo "Invalid extended regular expression: \`$unexpected'" \
      | batslib_decorate 'ERROR: refute_output' \
      | fail
    return $?
  fi

  # Matching.
  if (( is_mode_empty )); then
    if [ -n "$output" ]; then
      batslib_print_kv_single_or_multi 6 \
          'output' "$output" \
        | batslib_decorate 'output non-empty, but expected no output' \
        | fail
    fi
  elif (( is_mode_regexp )); then
    if [[ $output =~ $unexpected ]] || (( $? == 0 )); then
      batslib_print_kv_single_or_multi 6 \
          'regexp'  "$unexpected" \
          'output' "$output" \
        | batslib_decorate 'regular expression should not match output' \
        | fail
    fi
  elif (( is_mode_partial )); then
    if [[ $output == *"$unexpected"* ]]; then
      batslib_print_kv_single_or_multi 9 \
          'substring' "$unexpected" \
          'output'    "$output" \
        | batslib_decorate 'output should not contain substring' \
        | fail
    fi
  else
    if [[ $output == "$unexpected" ]]; then
      batslib_print_kv_single_or_multi 6 \
          'output' "$output" \
        | batslib_decorate 'output equals, but it was expected to differ' \
        | fail
    fi
  fi
}

# Fail and display details if the expected line is not found in the
# output (default) or in a specific line of it.
#
# By default, the entire output is searched for the expected line. The
# expected line is matched against every element of `${lines[@]}'. If no
# match is found, the assertion fails. Details include the expected line
# and `${lines[@]}'.
#
# When `--index <idx>' is specified, only the <idx>-th line is matched.
# If the expected line does not match `${lines[<idx>]}', the assertion
# fails. Details include <idx> and the compared lines.
#
# By default, literal matching is performed. A literal match fails if
# the expected string does not equal the matched string.
#
# Option `--partial' enables partial matching. A partial match fails if
# the expected substring is not found in the target string.
#
# Option `--regexp' enables regular expression matching. A regular
# expression match fails if the extended regular expression does not
# match the target string. An invalid regular expression causes an error
# to be displayed.
#
# It is an error to use partial and regular expression matching
# simultaneously.
#
# Mandatory arguments to long options are mandatory for short options
# too.
#
# Globals:
#   output
#   lines
# Options:
#   -n, --index <idx> - match the <idx>-th line
#   -p, --partial - partial matching
#   -e, --regexp - extended regular expression matching
# Arguments:
#   $1 - expected line
# Returns:
#   0 - match found
#   1 - otherwise
# Outputs:
#   STDERR - details, on failure
#            error message, on error
# FIXME(ztombol): Display `${lines[@]}' instead of `$output'!
assert_line() {
  local -i is_match_line=0
  local -i is_mode_partial=0
  local -i is_mode_regexp=0

  # Handle options.
  while (( $# > 0 )); do
    case "$1" in
      -n|--index)
        if (( $# < 2 )) || ! [[ $2 =~ ^([0-9]|[1-9][0-9]+)$ ]]; then
          echo "\`--index' requires an integer argument: \`$2'" \
            | batslib_decorate 'ERROR: assert_line' \
            | fail
          return $?
        fi
        is_match_line=1
        local -ri idx="$2"
        shift 2
        ;;
      -p|--partial) is_mode_partial=1; shift ;;
      -e|--regexp) is_mode_regexp=1; shift ;;
      --) shift; break ;;
      *) break ;;
    esac
  done

  if (( is_mode_partial )) && (( is_mode_regexp )); then
    echo "\`--partial' and \`--regexp' are mutually exclusive" \
      | batslib_decorate 'ERROR: assert_line' \
      | fail
    return $?
  fi

  # Arguments.
  local -r expected="$1"

  if (( is_mode_regexp == 1 )) && [[ '' =~ $expected ]] || (( $? == 2 )); then
    echo "Invalid extended regular expression: \`$expected'" \
      | batslib_decorate 'ERROR: assert_line' \
      | fail
    return $?
  fi

  # Matching.
  if (( is_match_line )); then
    # Specific line.
    if (( is_mode_regexp )); then
      if ! [[ ${lines[$idx]} =~ $expected ]]; then
        batslib_print_kv_single 6 \
            'index' "$idx" \
            'regexp' "$expected" \
            'line'  "${lines[$idx]}" \
          | batslib_decorate 'regular expression does not match line' \
          | fail
      fi
    elif (( is_mode_partial )); then
      if [[ ${lines[$idx]} != *"$expected"* ]]; then
        batslib_print_kv_single 9 \
            'index'     "$idx" \
            'substring' "$expected" \
            'line'      "${lines[$idx]}" \
          | batslib_decorate 'line does not contain substring' \
          | fail
      fi
    else
      if [[ ${lines[$idx]} != "$expected" ]]; then
        batslib_print_kv_single 8 \
            'index'    "$idx" \
            'expected' "$expected" \
            'actual'   "${lines[$idx]}" \
          | batslib_decorate 'line differs' \
          | fail
      fi
    fi
  else
    # Contained in output.
    if (( is_mode_regexp )); then
      local -i idx
      for (( idx = 0; idx < ${#lines[@]}; ++idx )); do
        [[ ${lines[$idx]} =~ $expected ]] && return 0
      done
      { local -ar single=(
          'regexp'  "$expected"
        )
        local -ar may_be_multi=(
          'output' "$output"
        )
        local -ir width="$( batslib_get_max_single_line_key_width \
                              "${single[@]}" "${may_be_multi[@]}" )"
        batslib_print_kv_single "$width" "${single[@]}"
        batslib_print_kv_single_or_multi "$width" "${may_be_multi[@]}"
      } | batslib_decorate 'no output line matches regular expression' \
        | fail
    elif (( is_mode_partial )); then
      local -i idx
      for (( idx = 0; idx < ${#lines[@]}; ++idx )); do
        [[ ${lines[$idx]} == *"$expected"* ]] && return 0
      done
      { local -ar single=(
          'substring' "$expected"
        )
        local -ar may_be_multi=(
          'output'    "$output"
        )
        local -ir width="$( batslib_get_max_single_line_key_width \
                              "${single[@]}" "${may_be_multi[@]}" )"
        batslib_print_kv_single "$width" "${single[@]}"
        batslib_print_kv_single_or_multi "$width" "${may_be_multi[@]}"
      } | batslib_decorate 'no output line contains substring' \
        | fail
    else
      local -i idx
      for (( idx = 0; idx < ${#lines[@]}; ++idx )); do
        [[ ${lines[$idx]} == "$expected" ]] && return 0
      done
      { local -ar single=(
          'line'   "$expected"
        )
        local -ar may_be_multi=(
          'output' "$output"
        )
        local -ir width="$( batslib_get_max_single_line_key_width \
                            "${single[@]}" "${may_be_multi[@]}" )"
        batslib_print_kv_single "$width" "${single[@]}"
        batslib_print_kv_single_or_multi "$width" "${may_be_multi[@]}"
      } | batslib_decorate 'output does not contain line' \
        | fail
    fi
  fi
}

# Fail and display details if the unexpected line is found in the output
# (default) or in a specific line of it.
#
# By default, the entire output is searched for the unexpected line. The
# unexpected line is matched against every element of `${lines[@]}'. If
# a match is found, the assertion fails. Details include the unexpected
# line, the index of the first match and `${lines[@]}' with the matching
# line highlighted if `${lines[@]}' is longer than one line.
#
# When `--index <idx>' is specified, only the <idx>-th line is matched.
# If the unexpected line matches `${lines[<idx>]}', the assertion fails.
# Details include <idx> and the unexpected line.
#
# By default, literal matching is performed. A literal match fails if
# the unexpected string does not equal the matched string.
#
# Option `--partial' enables partial matching. A partial match fails if
# the unexpected substring is found in the target string. When used with
# `--index <idx>', the unexpected substring is also displayed on
# failure.
#
# Option `--regexp' enables regular expression matching. A regular
# expression match fails if the extended regular expression matches the
# target string. When used with `--index <idx>', the regular expression
# is also displayed on failure. An invalid regular expression causes an
# error to be displayed.
#
# It is an error to use partial and regular expression matching
# simultaneously.
#
# Mandatory arguments to long options are mandatory for short options
# too.
#
# Globals:
#   output
#   lines
# Options:
#   -n, --index <idx> - match the <idx>-th line
#   -p, --partial - partial matching
#   -e, --regexp - extended regular expression matching
# Arguments:
#   $1 - unexpected line
# Returns:
#   0 - match not found
#   1 - otherwise
# Outputs:
#   STDERR - details, on failure
#            error message, on error
# FIXME(ztombol): Display `${lines[@]}' instead of `$output'!
refute_line() {
  local -i is_match_line=0
  local -i is_mode_partial=0
  local -i is_mode_regexp=0

  # Handle options.
  while (( $# > 0 )); do
    case "$1" in
      -n|--index)
        if (( $# < 2 )) || ! [[ $2 =~ ^([0-9]|[1-9][0-9]+)$ ]]; then
          echo "\`--index' requires an integer argument: \`$2'" \
            | batslib_decorate 'ERROR: refute_line' \
            | fail
          return $?
        fi
        is_match_line=1
        local -ri idx="$2"
        shift 2
        ;;
      -p|--partial) is_mode_partial=1; shift ;;
      -e|--regexp) is_mode_regexp=1; shift ;;
      --) shift; break ;;
      *) break ;;
    esac
  done

  if (( is_mode_partial )) && (( is_mode_regexp )); then
    echo "\`--partial' and \`--regexp' are mutually exclusive" \
      | batslib_decorate 'ERROR: refute_line' \
      | fail
    return $?
  fi

  # Arguments.
  local -r unexpected="$1"

  if (( is_mode_regexp == 1 )) && [[ '' =~ $unexpected ]] || (( $? == 2 )); then
    echo "Invalid extended regular expression: \`$unexpected'" \
      | batslib_decorate 'ERROR: refute_line' \
      | fail
    return $?
  fi

  # Matching.
  if (( is_match_line )); then
    # Specific line.
    if (( is_mode_regexp )); then
      if [[ ${lines[$idx]} =~ $unexpected ]] || (( $? == 0 )); then
        batslib_print_kv_single 6 \
            'index' "$idx" \
            'regexp' "$unexpected" \
            'line'  "${lines[$idx]}" \
          | batslib_decorate 'regular expression should not match line' \
          | fail
      fi
    elif (( is_mode_partial )); then
      if [[ ${lines[$idx]} == *"$unexpected"* ]]; then
        batslib_print_kv_single 9 \
            'index'     "$idx" \
            'substring' "$unexpected" \
            'line'      "${lines[$idx]}" \
          | batslib_decorate 'line should not contain substring' \
          | fail
      fi
    else
      if [[ ${lines[$idx]} == "$unexpected" ]]; then
        batslib_print_kv_single 5 \
            'index' "$idx" \
            'line'  "${lines[$idx]}" \
          | batslib_decorate 'line should differ' \
          | fail
      fi
    fi
  else
    # Line contained in output.
    if (( is_mode_regexp )); then
      local -i idx
      for (( idx = 0; idx < ${#lines[@]}; ++idx )); do
        if [[ ${lines[$idx]} =~ $unexpected ]]; then
          { local -ar single=(
              'regexp'  "$unexpected"
              'index'  "$idx"
            )
            local -a may_be_multi=(
              'output' "$output"
            )
            local -ir width="$( batslib_get_max_single_line_key_width \
                                "${single[@]}" "${may_be_multi[@]}" )"
            batslib_print_kv_single "$width" "${single[@]}"
            if batslib_is_single_line "${may_be_multi[1]}"; then
              batslib_print_kv_single "$width" "${may_be_multi[@]}"
            else
              may_be_multi[1]="$( printf '%s' "${may_be_multi[1]}" \
                                    | batslib_prefix \
                                    | batslib_mark '>' "$idx" )"
              batslib_print_kv_multi "${may_be_multi[@]}"
            fi
          } | batslib_decorate 'no line should match the regular expression' \
            | fail
          return $?
        fi
      done
    elif (( is_mode_partial )); then
      local -i idx
      for (( idx = 0; idx < ${#lines[@]}; ++idx )); do
        if [[ ${lines[$idx]} == *"$unexpected"* ]]; then
          { local -ar single=(
              'substring' "$unexpected"
              'index'     "$idx"
            )
            local -a may_be_multi=(
              'output'    "$output"
            )
            local -ir width="$( batslib_get_max_single_line_key_width \
                                "${single[@]}" "${may_be_multi[@]}" )"
            batslib_print_kv_single "$width" "${single[@]}"
            if batslib_is_single_line "${may_be_multi[1]}"; then
              batslib_print_kv_single "$width" "${may_be_multi[@]}"
            else
              may_be_multi[1]="$( printf '%s' "${may_be_multi[1]}" \
                                    | batslib_prefix \
                                    | batslib_mark '>' "$idx" )"
              batslib_print_kv_multi "${may_be_multi[@]}"
            fi
          } | batslib_decorate 'no line should contain substring' \
            | fail
          return $?
        fi
      done
    else
      local -i idx
      for (( idx = 0; idx < ${#lines[@]}; ++idx )); do
        if [[ ${lines[$idx]} == "$unexpected" ]]; then
          { local -ar single=(
              'line'   "$unexpected"
              'index'  "$idx"
            )
            local -a may_be_multi=(
              'output' "$output"
            )
            local -ir width="$( batslib_get_max_single_line_key_width \
                                "${single[@]}" "${may_be_multi[@]}" )"
            batslib_print_kv_single "$width" "${single[@]}"
            if batslib_is_single_line "${may_be_multi[1]}"; then
              batslib_print_kv_single "$width" "${may_be_multi[@]}"
            else
              may_be_multi[1]="$( printf '%s' "${may_be_multi[1]}" \
                                    | batslib_prefix \
                                    | batslib_mark '>' "$idx" )"
              batslib_print_kv_multi "${may_be_multi[@]}"
            fi
          } | batslib_decorate 'line should not be in output' \
            | fail
          return $?
        fi
      done
    fi
  fi
}
