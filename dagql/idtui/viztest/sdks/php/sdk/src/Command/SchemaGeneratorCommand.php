<?php

namespace Dagger\Command;

use Dagger\Codegen\SchemaGenerator;
use Dagger\Connection;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;

#[AsCommand('dagger:schema')]
class SchemaGeneratorCommand extends Command
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
        $client = $this->daggerConnection->connect();
        $generator = new SchemaGenerator($client);

        file_put_contents('schema.json', $generator->getJson());

        return Command::SUCCESS;
    }
}
