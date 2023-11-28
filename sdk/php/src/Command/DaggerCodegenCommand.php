<?php

namespace DaggerIo\Command;

use DaggerIo\Codegen\DaggerCodegen;
use DaggerIo\Codegen\SchemaGenerator;
use DaggerIo\Connection\DevDaggerConnection;
use DaggerIo\DaggerConnection;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;

#[AsCommand('dagger:codegen')]
class DaggerCodegenCommand extends Command
{
    private const WRITE_DIR =
        __DIR__.DIRECTORY_SEPARATOR.
        '..'.
        DIRECTORY_SEPARATOR.
        '..'.
        DIRECTORY_SEPARATOR.
        'generated';

    private DaggerConnection $daggerEngine;

    public function __construct()
    {
        parent::__construct();
        $this->daggerEngine = new DevDaggerConnection();
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);
        $client = $this->daggerEngine->getGraphQlClient();

        $schema = (new SchemaGenerator($client))->getSchema();
        $codegen = new DaggerCodegen($schema, self::WRITE_DIR, $io);
        $codegen->generate();

        return Command::SUCCESS;
    }
}
