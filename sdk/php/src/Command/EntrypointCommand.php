<?php

namespace Dagger\Command;

use Dagger\Client;
use Dagger\Codegen\Codegen;
use Dagger\Codegen\SchemaGenerator;
use Dagger\Connection;
use GraphQL\Utils\BuildClientSchema;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;

#[AsCommand('dagger:entrypoint')]
class EntrypointCommand extends Command
{
    private Connection $daggerConnection;

    public function __construct()
    {
        parent::__construct();
        $this->daggerConnection = Connection::get();
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);
        /** @var Client $client */
        //$client = $this->daggerConnection->connect();

        //$moduleName = $client->currentModule()->name();
        //$parentName = $client->currentFunctionCall()->parent()->getValue();

        //if ($parentName === "") {
            //register module with dagger
        //} else {
            //invocation, run module code.
        //}

        $io->info($this->findSrcDirectory());

        return Command::SUCCESS;
    }

    private function findSrcDirectory(): string
    {
        $dir = __DIR__;
        while(!file_exists($dir . '/dagger') && $dir !== '/') {
            $dir = realpath($dir . '/..');
        }

        if (!file_exists($dir . '/dagger') || !file_exists($dir . '/src')) {
            throw new \RuntimeException('Could not find module source directory');
        }

        return $dir . '/src';
    }
}
