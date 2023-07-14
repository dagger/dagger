> **Warning** This SDK is experimental. Please do not use it for anything
> mission-critical. Possible issues include:

- Missing features
- Stability issues
- Performance issues
- Lack of polish
- Upcoming breaking changes
- Incomplete or out-of-date documentation

# Dagger Ruby SDK

A client package for running [Dagger](https://dagger.io/) pipelines in Ruby.

## What is the Dagger Ruby SDK?

The Dagger Ruby SDK contains everything you need to develop CI/CD pipelines in Ruby, and run them on any OCI-compatible container runtime.

## Example

Ensure that you have [Dagger CLI installed](https://docs.dagger.io/cli/465058/install).
You will also need to have [Docker installed](https://docs.docker.com/engine/install/) and running.

Open a session to a Dagger Engine - the CLI will automatically provision the Dagger Engine in Docker if needed:

```console
dagger session

# A successful output will look similar to:
#
# Connected to engine 17ac956558e0
# {"port":62578,"session_token":"bc28086e-adc2-4609-88ba-9786fac83f1b"}
```

Leave the Dagger session running & create a `main.rb` file in a new shell session:

```ruby
client = Dagger.connect!

puts client
  .container
  .from("alpine")
  .with_exec("apk", "add", "curl")
  .with_exec("curl", "--version")
  .stdout
```

Run this file by providing it with the correct Dagger session port & token:

```console
DAGGER_SESSION_PORT="62578" DAGGER_SESSION_TOKEN="bc28086e-adc2-4609-88ba-9786fac83f1b" ruby main.rb

# A successful output will look similar to:
#
# curl 8.1.2 (x86_64-alpine-linux-musl) libcurl/8.1.2 OpenSSL/3.1.1 zlib/1.2.13 brotli/1.0.9 libidn2/2.3.4 nghttp2/1.53.0
# Release-Date: 2023-05-30
# Protocols: dict file ftp ftps gopher gophers http https imap imaps mqtt pop3 pop3s rtsp smb smbs smtp smtps telnet tftp ws wss
# Features: alt-svc AsynchDNS brotli HSTS HTTP2 HTTPS-proxy IDN IPv6 Largefile libz NTLM NTLM_WB SSL threadsafe TLS-SRP UnixSockets
```
