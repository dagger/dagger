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
    #[Doc('Send a query to a MariaDB service and return the response')]
    public function userList(
        #[Doc('host service')]
        Service $svc,
    ): string {
        return dag()
            ->container()
            ->from('mariadb:10.11.2')
            ->withServiceBinding('db', $svc)
            ->withExec([
                '/usr/bin/mysql',
                '--user=root',
                '--password=secret',
                '--host=db',
                '-e',
                'SELECT Host, User FROM mysql.user',
            ])
            ->stdout();
    }


}
