  # Container class
  class Container < Node
    extend T::Sig

    # @param opts - Optional arguments
    sig { params(opts: T.nilable(ContainerExecOpts)).returns(Container) }
    def exec(opts: nil)
      dag_node_args = {}
      unless opts.nil?
        dag_node_args['args'] = opts.args unless opts.args.nil?
        dag_node_args['stdin'] = opts.stdin unless opts.stdin.nil?
        dag_node_args['redirectStdout'] = opts.redirect_stdout unless opts.redirect_stdout.nil?
        dag_node_args['redirectStderr'] = opts.redirect_stderr unless opts.redirect_stderr.nil?
      end
      Container.new(self, @client, 'exec', dag_node_args)
    end

    sig { params(_blk: ContainerChain).returns(Container) }
    def with(&_blk)
      yield self
    end
  end