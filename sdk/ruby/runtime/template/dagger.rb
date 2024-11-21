# frozen_string_literal: true

require_relative 'main'

class HelloDagger
  def initialize
    @dag = Dagger.connect
  end

  def run(args)
    name = args.shift
    puts self.send(name.gsub('-', '_'), *args)
  end
end

HelloDagger.new.run ARGV
