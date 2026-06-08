<?php

require('./vendor/autoload.php');

use function Dagger\dag;

function test() {
    // set PHP versions against which to test
    $phpVersions = ['8.2', '8.3'];

    // get reference to the local project
    $src = dag()
        ->host()
        ->directory('.');

    foreach($phpVersions as $version) {
        $php = dag()
            ->container()
            // get container with specified PHP version
            ->from("php:$version")
            // mount source code into image
            ->withDirectory('/src', $src)
            // set current working directory for next commands
            ->withWorkdir('/src')
            // install composer
            ->withExec(['apt-get', 'update'])
            ->withExec(['apt-get', 'install', '--yes', 'git-core', 'zip', 'curl'])
            ->withExec(['sh', '-c', 'curl -sS https://getcomposer.org/installer | php -- --install-dir=/usr/local/bin --filename=composer'])
            // install dependencies
            ->withExec(['composer', 'install'])
            // run tests
            ->withExec(['./vendor/bin/phpunit']);

        // execute
        echo "Starting tests for PHP $version...\n";
        echo $php->stdout();
        echo "Completed tests for PHP $version\n**********\n";
    }
}

test();
