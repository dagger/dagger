require "json"
require "rbconfig"

module Dagger
  module Mod
    # Reports the error a module function failed with to the engine.
    #
    # An engine that names a file for the function's error, in
    # DAGGER_FUNCTION_ERROR_PATH, reads the error there and fails the call with
    # it, values and all, and shows it: the function's logs then get only a
    # short trace, through the module's own frames, in Ruby's own form, and
    # nothing for an error the engine shows already, an API error that names
    # its origin or one of the SDK's own. Otherwise, as with an older engine
    # or a file that can't be written, the error goes to stderr as Ruby prints
    # it, followed by one marker line with its message, which the generated
    # entrypoint reads.
    class ErrorReport
      # Starts the line of stderr that carries the function's error for an
      # engine that names no file for it.
      MARKER = "::dagger-function-error::".freeze

      # Names the file the engine gives a call's process for its function's error.
      PATH_ENV = "DAGGER_FUNCTION_ERROR_PATH".freeze

      # The largest function error file the engine reads.
      FILE_LIMIT = 1 << 20

      # What a report over FILE_LIMIT keeps of each long string, before it
      # drops the values.
      SHORTENED_LENGTH = 64 * 1024

      # The engine hands the entrypoint the last 100 KiB of a failed exec's
      # stderr, and JSON encodes a byte in at most six.
      MARKER_LIMIT = 16 * 1024

      # An origin an error's message names: the span of the call the engine
      # reports it from.
      ORIGIN = /\s*\[traceparent:[0-9a-f]{32}-[0-9a-f]{16}\]/

      # The encodings whose bytes are read as UTF-8.
      RAW = [Encoding::UTF_8, Encoding::BINARY].freeze

      # How Ruby shortens the trace of a stack overflow: the first frames, the
      # count of those it leaves out, the last frames (error.c, print_backtrace).
      TRACE_HEAD = 8
      TRACE_TAIL = 5
      TRACE_MAX = TRACE_HEAD + TRACE_TAIL + 5

      # The report for the module the process runs, whose code is everything
      # but the SDK, the clients generation writes, the installed gems (Gem.path
      # holds Bundler's path once bundler/setup ran) and Ruby's own library. A
      # path gem the module uses resolves to its own directory, so its frames
      # stay. Made before the function runs, which may change the working
      # directory.
      def self.for_module(module_root: Dir.pwd, sdk_root: File.expand_path("../../..", __dir__.to_s), env: ENV)
        roots = [sdk_root, File.join(module_root, "clients"), RbConfig::CONFIG["rubylibprefix"], *Gem.path]
        framework = roots.compact.flat_map { [it, real(it)] }.compact.map { "#{it.chomp("/")}/" }.uniq
        new(framework:, cwd: module_root, path: env[PATH_ENV])
      end

      def self.real(path)
        File.realpath(path)
      rescue SystemCallError
        nil
      end

      # framework: the directories whose code isn't the module's own, each
      # ending in /. path: the engine's file for the function's error, if any.
      def initialize(framework:, cwd:, path:, stderr: $stderr)
        @framework = framework
        @cwd = cwd
        @path = path.to_s.empty? ? nil : path
        @stderr = stderr
      end

      # Report error: in the engine's file when there is one and it can be
      # written, else with the marker, last. A hint, if any, follows the
      # error's own output.
      def report(error, hint: nil)
        written = write_file(error)
        if written
          @stderr.write(short_trace(error)) if shows_trace?(error)
        else
          @stderr.write(full_trace(error))
        end
        @stderr.puts(hint) if hint
        write_marker(error) unless written
        @stderr.flush
      end

      # The report the engine reads: {"message": ..., "values": {...}}, the
      # error's message in full, with any origin it names, and the extensions
      # of the API error it is or was raised from, as they are, such as a
      # failed exec's exit code and output. Only a report over what the engine
      # reads is shortened: long strings keep their end, where output usually
      # says what failed, and the message its start and the origins it names;
      # then the values go.
      def self.file_report(error)
        message = utf8(message_of(error))
        values = utf8(values(error))
        report = encode(message, values)
        return report if report.bytesize <= FILE_LIMIT

        message = shorten_message(message, SHORTENED_LENGTH)
        report = encode(message, shortened(values))
        return report if report.bytesize <= FILE_LIMIT

        encode(message, {})
      end

      # The error as Ruby prints one it doesn't rescue, causes after it, each
      # through the module's own frames only, and without the origins the
      # messages name. An error without a frame of the module's, such as a
      # file of it that doesn't parse, is Ruby's message alone, with what Ruby
      # prints beside it, such as syntax_suggest's section.
      def short_trace(error)
        self.class.chain(error).map { describe(it) }.join
      end

      # Whether the function's logs show the error. The engine shows an API
      # error that names its origin with the logs of the call that failed, and
      # the message of an error of the SDK's own, which has no frame of the
      # module's; an error of the module's own, even one raised from such an
      # API error, is the module's to show.
      def shows_trace?(error)
        return false if error.is_a?(Mod::Error)

        !(error.is_a?(::Dagger::QueryError) && ORIGIN.match?(self.class.utf8(error.message)))
      end

      # The error's message, or its class when it has none: the engine reads
      # only a JSON report with a message, and rejects one with an empty
      # message as invalid. A SyntaxError's message is the file's annotated
      # source, which the function's logs show; its first line names the file
      # and the line.
      def self.message_of(error)
        message = utf8(error.message)
        message = message.lines.first.to_s.chomp if error.is_a?(SyntaxError)
        message.strip.empty? ? error.class.name.to_s : message
      end

      # The extensions of the first API error in the error's chain of causes.
      def self.values(error)
        query_error = chain(error).find { it.is_a?(::Dagger::QueryError) }
        extensions = query_error.is_a?(::Dagger::QueryError) ? query_error.extensions : nil
        extensions.is_a?(Hash) ? extensions : {}
      end

      def self.chain(error)
        errors = [] #: Array[Exception]
        current = error #: Exception?
        while current && !errors.include?(current)
          errors << current
          current = current.cause
        end
        errors
      end

      # The message cut to length bytes, with the origins it names after the cut.
      def self.shorten_message(message, length)
        return message if message.bytesize <= length

        origins = (message.byteslice(length..) || "").scan(ORIGIN).join
        "#{message.byteslice(0, length).to_s.scrub("")}...#{origins}"
      end

      # The value with each string longer than SHORTENED_LENGTH, at any depth,
      # cut to its end.
      def self.shortened(value)
        case value
        when String
          return value if value.bytesize <= SHORTENED_LENGTH

          "...#{value.byteslice(-SHORTENED_LENGTH..).to_s.scrub("")}"
        when Hash then value.transform_values { shortened(it) }
        when Array then value.map { shortened(it) }
        else value
        end
      end

      # The value with each string in UTF-8, what can't be read replaced: a
      # message may hold any bytes, such as a command's output.
      def self.utf8(value)
        case value
        when String
          return value.encode(Encoding::UTF_8, invalid: :replace, undef: :replace) unless RAW.include?(value.encoding)

          value.dup.force_encoding(Encoding::UTF_8).scrub
        when Hash then value.to_h { |key, item| [utf8(key.to_s), utf8(item)] }
        when Array then value.map { utf8(it) }
        else value
        end
      end

      def self.encode(message, values) = JSON.generate({ "message" => message, "values" => values })

      private

      def write_file(error)
        path = @path or return false
        report = self.class.file_report(error)
        File.binwrite(path, report) == report.bytesize
      rescue SystemCallError, IOError, JSON::GeneratorError
        false
      end

      # For an engine that reads no file: the error as Ruby prints it, whole,
      # or an API error's or the SDK's message, with a failed exec's stderr,
      # as such an engine shows no exec's logs with its error.
      def full_trace(error)
        return "#{without_origins(error.full_message(highlight: false)).rstrip}\n" unless sdk_error?(error)

        text = "dagger-mod: #{error.class}: #{without_origins(error.message)}\n"
        stderr = error.is_a?(::Dagger::ExecError) ? error.stderr.to_s : ""
        stderr.empty? ? text : "#{text}#{stderr.chomp}\n"
      end

      def write_marker(error)
        message = self.class.shorten_message(self.class.utf8(self.class.message_of(error)), MARKER_LIMIT)
        @stderr.puts("#{MARKER}#{JSON.generate(message)}")
      end

      def sdk_error?(error) = error.is_a?(Mod::Error) || error.is_a?(::Dagger::QueryError)

      # One error of the chain, as Ruby's full_message gives it: the first
      # frame, the message and its class, then a line for each other frame,
      # those of a stack overflow shortened as Ruby shortens them.
      def describe(error)
        frames = module_frames(error)
        text = without_origins(detailed_message(error, raised_here: frames.first == error.backtrace&.first)).rstrip
        return "#{text}\n" if frames.empty?

        "#{frames.first}: #{text}\n#{from_lines(error, frames)}"
      end

      def from_lines(error, frames)
        lines = frames.drop(1).map { "\tfrom #{it}\n" }
        return lines.join unless error.is_a?(SystemStackError) && frames.size > TRACE_MAX

        skipped = frames.size - TRACE_MAX + 1
        [*lines.first(TRACE_HEAD), "\t ... #{skipped} levels...\n", *lines.drop(TRACE_HEAD + skipped)].join
      end

      # Ruby's message and class, as Ruby prints them: a SyntaxError's with
      # syntax_suggest's section before it. error_highlight's snippet is of the
      # frame that raised, so it goes when that frame is not the module's,
      # which heads the trace instead.
      def detailed_message(error, raised_here:)
        error.detailed_message(highlight: false, error_highlight: raised_here)
      end

      # A frame's path is its location's, when the frame is still the one
      # Ruby recorded, and else the start of its line: a backtrace set since
      # need not drop the locations it replaced.
      def module_frames(error)
        lines = error.backtrace || []
        locations = error.backtrace_locations || []
        lines.each_with_index.filter_map do |line, index|
          location = locations[index]
          path = location&.to_s == line ? location&.path : line[/\A(.+?):\d+(?::|\z)/, 1]
          line if module_file?(path)
        end
      end

      def module_file?(path)
        return false if path.nil? || path.empty? || path.start_with?("<", "(")

        full = File.expand_path(path, @cwd)
        @framework.none? { full.start_with?(it) }
      end

      def without_origins(text) = self.class.utf8(text).gsub(ORIGIN, "")
    end
  end
end
