<?php

namespace Dagger\Command;

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

#[AsCommand('dagger:codegen')]
class CodegenCommand extends Command
{
    private const WRITE_DIR =
        __DIR__ . DIRECTORY_SEPARATOR .
        '..' .
        DIRECTORY_SEPARATOR .
        '..' .
        DIRECTORY_SEPARATOR .
        'generated';

    private Connection $daggerConnection;

    public function __construct()
    {
        parent::__construct();
        $this->daggerConnection = Connection::get();
    }

    public function configure(): void
    {
        $this->addOption(
            'schema-file',
            null,
            InputArgument::OPTIONAL,
            'Path to the schema json file',
            null
        );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);


        if ($input->getOption('schema-file') !== null && file_exists($input->getOption('schema-file'))) {
            $fileContents = file_get_contents($input->getOption('schema-file'));
            $schemaArray = json_decode($fileContents, true);
            $schema = BuildClientSchema::build($schemaArray);
        } else {
            $client = $this->daggerConnection->connect();
            $schema = (new SchemaGenerator($client))->getSchema();
        }

        $codegen = new Codegen($schema, self::WRITE_DIR, $io);
        $codegen->generate();

        return Command::SUCCESS;
    }
}
