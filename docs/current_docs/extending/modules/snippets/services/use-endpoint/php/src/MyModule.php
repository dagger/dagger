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
    public function get(): string
    {
        // start NGINX service
        $service = dag()->container()->from('nginx')->withExposedPort(80)->asService();
        $service->start();

        // wait for service to be ready
        $endpoint = $service->endpoint(80, 'http');

        // send HTTP request to service endpoint
        return dag()->http($endpoint)->contents();
    }
}
