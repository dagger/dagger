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

# dagger-php-sdk
Experimental [Dagger.io](https://dagger.io) SDK for PHP

## Usage

### Install the composer package

TODO

### Example code

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
