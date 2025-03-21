<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\DefaultPath;
use Dagger\Attribute\Doc;
use Dagger\Container;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class HelloDagger
{
    #[DaggerFunction]
    #[Doc('Publish the application container after building and testing it on-the-fly')]
    public function publish(
      #[DefaultPath('/')]
      Directory $source,
    ): string {
        // call Dagger Function to run unit tests
        $this->test($source);

        // call Dagger Function to build the application image
        // publish the image to ttl.sh
        return $this
            ->build($source)
            ->publish('ttl.sh/hello-dagger-' . rand(0, 10000000));
    }
}
