# Updating Dagger

## Summary
 - [Option 1: (Mac OS only): update from Homebrew](#option-1--mac-os-only---update-from-homebrew)
 - [Option 2: update from a shell script](#option-2--update-from-a-shell-script)
 - [Option 3: Manually fetch the latest binary release from Github](#option-3--manually-fetch-the-latest-binary-release-from-github)
 - [Option 4: Compile from source](#option-4--compile-from-source)

## Option 1: (Mac OS only): update from Homebrew

From your Mac OS terminal, run the following command:

```shell
brew update dagger/tap/dagger
```

> :bulb: Verify the update with `dagger version`.

## Option 2: update from a shell script

From a terminal, run the following command:

```shell
curl -sfL https://releases.dagger.io/dagger/install.sh | sh
```

You now have the dagger binary in the local directory under `./bin/dagger`.

You can then update it globally on your system:

```shell
sudo mv ./bin/dagger /usr/local/bin
```

> :bulb: Verify the update with `dagger version`.

## Option 3: Manually fetch the latest binary release from Github

Open your web browser to [the latest release](https://github.com/dagger/dagger/releases/latest).

From the **assets** section, download the archive corresponding to your OS and Arch.

> :bulb: Verify the update with `dagger version`.

## Option 4: Compile from source

You will need [Go](https://golang.org) version 1.16 or later.

1. Clone the dagger repository

```shell
git clone https://github.com/dagger/dagger.git
```

2. Build the `dagger` binary.

```shell
cd dagger; make
```

3. Copy the `dagger` tool to a location listed in your `$PATH`. For example, to copy it to `/usr/local/bin`:

```shell
cp ./cmd/dagger/dagger /usr/local/bin
```

> :bulb: Verify the update with `dagger version`.