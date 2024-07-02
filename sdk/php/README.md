> **Warning** This SDK is experimental. Please do not use it for anything
> mission-critical. Possible issues include:

- Missing features
- Stability issues
- Performance issues
- Lack of polish
- Upcoming breaking changes
- Incomplete or out-of-date documentation

> **Warning**
> The Dagger PHP SDK requires Dagger v0.9.3 or later

# Dagger PHP SDK
An experimental [Dagger.io](https://dagger.io) SDK for PHP.

## Usage

```php
$client = Dagger::connect();
$output = $client->pipeline('test')
    ->container()
    ->from('alpine')
    ->withExec(['apk', 'add', 'curl'])
    ->withExec(['curl', 'https://dagger.io'])
    ->stdout();

echo substr($output, 0, 300);
```

## Development environment

You can launch a basic development environment by using the provided docker-compose file.

1. Launch the cli : `docker compose up -d cli`
2. Spawn a shell inside : `docker compose exec cli bash`
3. Install dependencies : `composer install`
4. Run the tests : `phpunit`

You can regenerate the files by using the `./codegen` command

## Developing with the PHP SDK runtime

From a parent directory of the PHP SDK, run `dagger init --sdk=<path to dagger repo>/sdk/php`.

This will use the PHP SDK runtime with local source code which will make the feedback loop much faster than
pulling changes from the remote repository.