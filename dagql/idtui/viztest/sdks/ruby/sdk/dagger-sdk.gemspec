Gem::Specification.new do |spec|
  spec.name = "dagger-sdk"
  spec.version = "0.0.0"
  spec.summary = "The Dagger SDK for Ruby: modules, generated clients and the engine session"
  spec.authors = ["Dagger"]
  spec.email = ["hello@dagger.io"]
  spec.homepage = "https://github.com/dagger/ruby-sdk"
  spec.license = "Apache-2.0"
  spec.required_ruby_version = ">= 3.4"
  spec.files = Dir.chdir(__dir__) { Dir["lib/**/*.rb", "sig/**/*.rbs", "exe/*", "LICENSE"] }
  spec.require_paths = ["lib"]
  spec.bindir = "exe"
  spec.executables = ["dagger-mod"]
  spec.metadata["rubygems_mfa_required"] = "true"
end
