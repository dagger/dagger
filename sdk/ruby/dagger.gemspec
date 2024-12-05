Gem::Specification.new do |s|
  s.name = 'dagger-sdk'
  s.version = '0.0.0'
  s.summary = 'Dagger Ruby SDK'
  s.authors = ['Dagger Team']
  s.require_paths = ['lib']
  s.files = Dir['lib/**/*rb']
  s.homepage = 'https://dagger.io'
  s.license = 'Apache-2.0'
  s.add_dependency 'base64', '~> 0.2.0'
  s.add_dependency 'sorbet-runtime', '~> 0.5.0'
  s.add_dependency 'json', '~> 2.8.2'
  s.add_development_dependency 'sorbet', '~> 0.5.0'
  s.add_development_dependency 'tapioca', '~> 0.16.0'
end
