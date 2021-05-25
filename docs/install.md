# Installing or upgrading Dagger

## Option 1: Use Homebrew (Mac OS only)

From your Mac OS terminal, run the following command:

```shell
brew install dagger/tap/dagger
```

If dagger is already installed, you can upgrade it using this command:

```shell
brew update; brew upgrade dagger
```

## Option 2: Run a shell script

From a terminal, run the following command:

```shell
curl -sfL https://releases.dagger.io/dagger/install.sh | sh
```

You now have the dagger binary in the local directory under `./bin/dagger`.

You can then install it globally on your system:

```shell
sudo mv ./bin/dagger /usr/local/bin
```

## Option 3: Download a binary release

Open your web browser to [the latest release](https://github.com/dagger/dagger/releases/latest).

From the **assets** section, download the archive corresponding to your OS and Arch.

## Option 4: Install from source

You will need [Go](https://golang.org) version 1.16 or later.

1\. Clone the dagger repository

```shell
git clone https://github.com/dagger/dagger.git
```

2\. Build the `dagger` binary.

```shell
cd dagger; make
```

3\. Copy the `dagger` tool to a location listed in your `$PATH`. For example, to copy it to `/usr/local/bin`:

```shell
cp ./cmd/dagger/dagger /usr/local/bin
```
