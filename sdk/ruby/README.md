# Dagger Ruby SDK

A client package for running [Dagger](https://dagger.io) pipelines.

## What is the Dagger Ruby SDK?

The Dagger Ruby SDK contains everything you need to develop CI/CD pipeline in Ruby, and run them on any OCI-compatible container runtime.

## Install

```
gem install dagger
```

Or add `gem 'dagger-sdk'` to your `Gemfile` and run `bundle install`.


## Local development

### 1. Create a new project

```
mkdir my-test-rb-project
cd my-test-rb-project

# Initialize Gemfile
bundle init
```

Edit the `Gemfile`:

```ruby
# frozen_string_literal: true

source "https://rubygems.org"

gem "dagger-sdk"
```

Fetch dependencies

```
bundle install
```

### 2. Create an example application

Edit a `main.rb` file as follow:

```ruby
# frozen_string_literal: true

require 'dagger'

def hello_world
  dag = Dagger.connect

  dag
    .container
    .from(address: 'alpine:latest')
    .with_exec(args: %w(echo Hello Dagger!))
    .stdout
end

puts hello_world
```

Run it:

```
$ dagger run -q bundle exec ruby main.rb
Hello Dagger!
```
