require "json"

module Dagger
  # Starts `dagger session` for a plain program that runs with no session in
  # its environment, and ends it when the program closes the session.
  class Provision
    LABELS = ["--label", "dagger.io/sdk.name:ruby"].freeze
    STDERR_LINES = 200

    attr_reader :connection

    def self.cli = ENV.fetch("_EXPERIMENTAL_DAGGER_CLI_BIN", nil) || "dagger"

    def initialize(cli: self.class.cli)
      @stdin, @stdout, @stderr, @wait = popen(cli)
      @pid = @wait[:pid]
      @stderr_tail = []
      @drain = Thread.new { @stderr.each_line { tail(it) } }
      @connection = connect
    rescue SystemCallError => e
      raise ConnectionError, "cannot start #{cli} session: #{e.message}"
    end

    # Closing stdin ends the session and lets it drain its logs first.
    def close
      @stdin.close unless @stdin.closed?
      @wait.join(300) || Process.kill(:KILL, @pid)
      @drain.join(1)
    end

    private

    def popen(cli)
      stdin, stdout, stderr, wait = Open3.popen3(cli, "session", *LABELS)
      [stdin, stdout, stderr, wait]
    end

    def connect
      line = @stdout.gets
      if line.nil?
        @wait.join
        @drain.join(1)
        raise ConnectionError, "dagger session exited: #{@stderr_tail.join.strip}"
      end
      params = JSON.parse(line)
      Connection.new(port: Integer(params.fetch("port")), token: params.fetch("session_token"))
    rescue JSON::ParserError, KeyError, ArgumentError => e
      raise ConnectionError, "dagger session printed no connection parameters: #{e.message}"
    end

    def tail(line)
      @stderr_tail << line
      @stderr_tail.shift if @stderr_tail.size > STDERR_LINES
    end
  end
end
