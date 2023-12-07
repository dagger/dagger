<?php

namespace DaggerIo\Command;

use DaggerIo\Codegen\SchemaGenerator;
use DaggerIo\Connection\DevDaggerConnection;
use DaggerIo\Dagger;
use DaggerIo\DaggerConnection;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;

#[AsCommand('dagger:schema')]
class DaggerSchemaGeneratorCommand extends Command
{
    private const WRITE_DIR =
        __DIR__.DIRECTORY_SEPARATOR.
        '..'.
        DIRECTORY_SEPARATOR.
        '..'.
        DIRECTORY_SEPARATOR.
        'generated';

    private DaggerConnection $daggerConnection;

    public function __construct()
    {
        parent::__construct();
        $this->daggerConnection = DaggerConnection::newProcessSession('.', Dagger::DEFAULT_CLI_VERSION);
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);
        $client = $this->daggerConnection->getGraphQlClient();
        $generator = new SchemaGenerator($client);

        file_put_contents('schema.json', $generator->getJson());

        return Command::SUCCESS;
    }
}
