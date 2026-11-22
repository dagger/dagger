require "dagger"
require "dagger/clients/roots"

# The external Ruby SDK's fixture for the golden tests: every SDK fixture has
# the same functions, so their goldens line up.
class ViztestRuby
  extend Dagger::Mod::Object
  include Dagger::Clients::Roots

  function "Logs to stdout and stderr, and succeeds.", cache: "session"
  #: () -> String
  def log_success
    log_both
    "ruby function result"
  end

  function "Logs to stdout and stderr, then raises an error of its own.", cache: "session"
  #: () -> void
  def fail_native
    log_both
    raise "ruby function failed natively"
  end

  function "Logs to stdout and stderr, then exits the process with a custom code.", cache: "session"
  #: () -> void
  def fail_exit_code
    log_both
    exit 3
  end

  function "Runs a command that fails, and lets its error propagate.", cache: "session"
  #: () -> String
  def fail_exec = failing_exec.stdout

  function "Runs a command that fails, and wraps its error.", cache: "session"
  #: () -> String
  def fail_exec_wrapped
    failing_exec.stdout
  rescue Dagger::ExecError => e
    raise "ruby function ran a failing command: #{e.message}"
  end

  function "Logs a line, then reads a file that doesn't exist, and lets the error propagate.", cache: "session"
  #: () -> String
  def fail_api
    log_stdout
    core.directory.file(path: "doesnt-exist.txt").contents
  end

  function "Calls a function of a dependency whose command fails.", cache: "session"
  #: () -> String
  def call_failing_dep = sdk_dep.fail_exec

  function "Logs a line, then calls a function of a dependency whose command fails.", cache: "session"
  #: () -> String
  def call_failing_dep_logged
    log_stdout
    sdk_dep.fail_exec
  end

  private

  #: () -> Dagger::Clients::Core::Container
  def failing_exec
    core.container.from(address: "alpine")
        .with_exec(args: ["sh", "-c", "echo exec stdout; sleep 0.1; echo exec stderr >&2; exit 1"])
  end

  #: () -> void
  def log_stdout
    $stdout.puts "ruby function stdout"
    $stdout.flush
  end

  # Logs a line to stdout, then one to stderr. The streams are captured
  # apart, so the pause keeps their order stable in the goldens.
  #: () -> void
  def log_both
    log_stdout
    sleep 0.1
    $stderr.puts "ruby function stderr"
    $stderr.flush
  end
end
