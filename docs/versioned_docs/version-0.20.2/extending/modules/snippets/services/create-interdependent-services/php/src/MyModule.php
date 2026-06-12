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
    #[Doc('Run two services which are dependent on each other')]
    public function services(): Service
    {
        $svcA = dag()
            ->container()
            ->from('nginx')
            ->withExposedPort(80)
            ->asService(args: [
                'sh',
                '-c',
                'nginx & while true; do curl svcb:80 && sleep 1; done'
            ])
            ->withHostname('svca');

        $svcA->start();

        $svcB = dag()
            ->container()
            ->from('nginx')
            ->withExposedPort(80)
            ->asService(args: [
                'sh',
                '-c',
                'nginx & while true; do curl svca:80 && sleep 1; done'
            ])
            ->withHostname('svcb');

        $svcB->start();

        return $svcB;
    }


}
