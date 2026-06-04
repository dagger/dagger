<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, Doc};
use Dagger\Service;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Start and return an HTTP service')]
    public function httpService(): Service
    {
        return dag()
            ->container()
            ->from('python')
            ->withWorkdir('/srv')
            ->withNewFile('index.html', 'Hello, world!')
            ->withExposedPort(8080)
            ->asService(args: ['python', '-m', 'http.server', '8080']);
    }

    #[DaggerFunction]
    #[Doc('Send a request to an HTTP service and return the response')]
    public function get(): string
    {
        return dag()
            ->container()
            ->from('alpine')
            ->withServiceBinding('www', $this->httpService())
            ->withExec(['wget', '-O-', 'http://www:8080'])
            ->stdout();
    }
}
