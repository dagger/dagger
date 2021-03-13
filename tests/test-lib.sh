#!/usr/bin/env bash
set -o errexit -o errtrace -o functrace -o nounset -o pipefail

########################################################################
# Logging helpers
########################################################################
readonly COLOR_RED=1
readonly COLOR_GREEN=2
readonly COLOR_YELLOW=3

# Prefix a date to a log line and output to stderr
logger::stamp(){
  local color="$1"
  local level="$2"
  local i
  shift
  shift
  [ ! "$TERM" ] || [ ! -t 2 ] || >&2 tput setaf "$color"
  for i in "$@"; do
    >&2 printf "[%s] [%s] %s\\n" "$(date)" "$level" "$i"
  done
  [ ! "$TERM" ] || [ ! -t 2 ] || >&2 tput op
}

logger::info(){
  logger::stamp "$COLOR_GREEN" "INFO" "$@"
}

logger::warning(){
  logger::stamp "$COLOR_YELLOW" "WARNING" "$@"
}

logger::error(){
  logger::stamp "$COLOR_RED" "ERROR" "$@"
}

########################################################################
# Handle exit code and errors of random command
########################################################################
wrap::err(){
  local args=()
  local actualExit=0
  local expectedExit=0
  local ret=0

  for i in "$@"; do
    if [ "${i:0:9}" == "--stderr=" ]; then
      local expectedStderr="${i:9}"
    elif [ "${i:0:7}" == "--exit=" ]; then
      expectedExit="${i:7}"
      expectedExit="${expectedExit:-0}"
    else
      args+=("$i")
    fi
  done

  logger::info " -> ${args[*]}"

  exec 3>&1
  actualStderr="$("${args[@]}" 2>&1 1>&3)" || actualExit="$?"

  [ "$expectedExit" == "$actualExit" ] || {
    logger::error " -> Expected exit code: $expectedExit" " -> Actual exit code  : $actualExit"
    logger::error " -> Stderr was:"
    >&2 jq <<<"$actualStderr" 2>/dev/null || {
      >&2 echo "$actualStderr"
      logger::error " -> Also, stderr is not json"
    }
    ret=1
  }

  [ -z "${expectedStderr+x}" ] || [ "$expectedStderr" == "$actualStderr" ] || {
    logger::error " -> Expected stderr:"
    >&2 jq <<<"$expectedStderr" 2>/dev/null || {
      >&2 echo "$expectedStderr"
    }
    logger::error " -> Actual stderr  :"
    >&2 jq <<<"$actualStderr" 2>/dev/null || {
      >&2 echo "$actualStderr"
      logger::error " -> Also, stderr is not json ^"
    }
    ret=1
  }

  exec 3>&-
  return "$ret"
}

########################################################################
# Main test function
#    argument 1 is a test description
#    to test the exit code, pass --exit=int (if not provided, will test that the command exits succesfully)
#    to test the value of stderr, pass --stderr=string (if not provided, stderr is not verified)
#    to test the value of stdout, pass --stdout=string (if not provided, stdout is not verified)
#    any other argument is the command that is going to be run
# Example:
#    test dagger compute somecue --exit=1 --stderr=expectederror
########################################################################
test::one(){
  local testDescription="$1"
  shift
  local args=()
  local ret=0

  for i in "$@"; do
    if [ "${i:0:9}" == "--stdout=" ]; then
      local expectedStdout="${i:9}"
    else
      args+=("$i")
    fi
  done

  logger::info "$testDescription"

  local actualStdout
  actualStdout="$(wrap::err "${args[@]}")" || {
    ret=1
  }

  [ -z "${expectedStdout+x}" ] || [ "$expectedStdout" == "$actualStdout" ] || {
    exec 3>&-
    logger::error " -> Expected stdout: $expectedStdout" " -> Actual stdout  : $actualStdout"
    ret=1
  }

  [ "$ret" != 0 ] || logger::info " -> Success"
  return "$ret"
}

disable(){
  logger::warning "Test \"$2\" has been disabled."
}

secret(){
  if [ -z "${DAGGER_SECRETS_LOADED+x}" ] || [ "$DAGGER_SECRETS_LOADED" != "1" ]; then
    logger::warning "Skip \"$2\": secrets not available"
  else
    "$@"
  fi
}
