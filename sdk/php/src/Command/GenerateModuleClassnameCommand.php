<?php

declare(strict_types=1);

namespace Dagger\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;

#[AsCommand('dagger:generate-module-classname')]
class GenerateModuleClassnameCommand extends Command
{
    public function configure(): void
    {
        $this->addArgument('classname', InputArgument::REQUIRED, 'Classname for the module');
    }
    protected function execute(
        InputInterface $input,
        OutputInterface $output
    ): int {

        $classname = $this->toSuitableClassname($input->getArgument('classname'));

        if (is_numeric($classname[0])) {
            throw new \ValueError('Module name cannot begin with a number');
        }

        $output->writeln($classname);

        return Command::SUCCESS;
    }

    private function toSuitableClassname(string $value): string
    {
        $whiteSpaceSeperatedValue = preg_replace('#[\s\-_]#', ' ', $value);

        assert(is_string($whiteSpaceSeperatedValue));
        $capitilisedValue = ucwords($whiteSpaceSeperatedValue);

        $pascalValue = preg_replace('#\s#', '', $capitilisedValue);
        return preg_replace('#[^a-zA-Z0-9]#', '', $pascalValue);
    }
}
