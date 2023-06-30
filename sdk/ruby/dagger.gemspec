# frozen_string_literal: true

require_relative "lib/dagger/version"

Gem::Specification.new do |spec|
  spec.name        = "dagger"
  spec.summary     = "Ruby SDK for Dagger"
  spec.files       = Dir["lib/**/*.rb"]
  spec.version     = Dagger::VERSION
  spec.required_ruby_version = ">= 2.5"
end