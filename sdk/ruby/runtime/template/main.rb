#!/usr/bin/env ruby

$LOAD_PATH.unshift('sdk', 'lib')

require 'dagger_module'

def run(args)
  name = args.shift
  puts DaggerModule.new.send(name.gsub('-', '_'), *args)
end

run(ARGV)
