#!/usr/bin/env php
<?php declare(strict_types=1);

use Dagger\Command\GenerateModuleClassnameCommand;
use Symfony\Component\Console\Application;
use Dagger\Command\CodegenCommand;
use Dagger\Command\SchemaGeneratorCommand;

if (file_exists(__DIR__.'/../../autoload.php')) {
    // The usual location, since this file will reside in vendor/bin
    require __DIR__.'/../../autoload.php';
} else {
    // Useful when doing development on this package
    require __DIR__.'/vendor/autoload.php';
}

$console = new Application();

$console->add(new CodegenCommand());
$console->setDefaultCommand('dagger:codegen');
$console->add(new SchemaGeneratorCommand());
$console->add(new GenerateModuleClassnameCommand());

$console->run();