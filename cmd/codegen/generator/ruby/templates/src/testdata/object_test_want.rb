  class Container < Node
    # @return [Container]
    def exec(args: nil, stdin: nil, redirect_stdout: nil, redirect_stderr: nil)
      args = {}
      args['args'] = args unless args.nil?
      args['stdin'] = stdin unless stdin.nil?
      args['redirectStdout'] = redirect_stdout unless redirect_stdout.nil?
      args['redirectStderr'] = redirect_stderr unless redirect_stderr.nil?
      Container.new(self, @client, 'exec', args)
    end

    def with(fun)
      fun.call(self)
    end
  end