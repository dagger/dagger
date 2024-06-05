<?php

namespace Dagger\Command;

use Dagger\Attribute\DaggerObject;
use Dagger\Client;
use Dagger\Connection;
use Roave\BetterReflection\BetterReflection;
use Roave\BetterReflection\Reflector\DefaultReflector;
use Roave\BetterReflection\SourceLocator\Type\DirectoriesSourceLocator;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Dagger\Dagger;
use Dagger\Client as DaggerClient;
use ReflectionClass;
use ReflectionMethod;

#[AsCommand('dagger:entrypoint')]
class EntrypointCommand extends Command
{
    private DaggerClient $daggerConnection;

    public function __construct()
    {
        parent::__construct();
        $this->daggerConnection = Dagger::connect();
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);
        /** @var Client $client */

        $io->info('==----=-==-=-=-= CUSTOM CODEEEE ==----=-==-=-=-=');

        // $moduleName = $this->daggerConnection->module()->id();
        // $moduleName = $this->daggerConnection->module()->name();
        // $io->info('MODULE NAME: ' . $moduleName);

        $parentName = $this->daggerConnection->currentFunctionCall()->parent()->getValue();

        if (!$this->hasParentName($parentName)) {
            $io->info('NO PARENT NAME FOUND');
            // register module with dagger
        } else {
            $io->info('!!!!! FOUND A PARENT NAME: ' . $parentName);
            // invocation, run module code.
        }

        $dir = $this->findSrcDirectory();
        $classes = $this->getDaggerObjects($dir);
        $io->info(var_export($classes, true));

        foreach($classes as $class) {

            $io->info('FOUND CLASS WITH DaggerFunction annotation: ' . $class);

            // Loop thru all the functions in this class
            $reflectedClass = new ReflectionClass($class);
            $reflectedMethods = $reflectedClass->getMethods(ReflectionMethod::IS_PUBLIC);
            // $io->info(var_export($reflectedMethods, true));

            foreach($reflectedMethods as $method) {
                $methodName = $method->getName();
                $io->info('FOUND METHOD: ' . $methodName);
                $methodAttributes = $method->getAttributes();
                foreach($methodAttributes as $methodAttribute) {
                    $io->info('FOUND METHOD ATTRIBUTE: ' . $methodAttribute->getName());
                }
                // $io->info(var_export($methodAttributes, true));
            }

            // $reflectionMethod = new ReflectionMethod($reflectedClass->, 'myMethod');

            // // Get the attributes of the method
            // $attributes = $reflectionMethod->getAttributes();
            
            // foreach ($attributes as $attribute) {
            //     $attributeInstance = $attribute->newInstance();
            //     echo 'Attribute class: ' . $attribute->getName() . PHP_EOL;
            //     echo 'Attribute value: ' . $attributeInstance->value . PHP_EOL;
            // }

            



            
            // Find functions tagged with [DaggerFunction]
            // create a ->withFunction entry
            // Find the args on the function, and do ->withArg() on it

            /*$client->module()->withObject(
                $client->typeDef()->withFunction(
                    $client->function()
                        ->withArg()
                )
            );*/
        }

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

    private function getDaggerObjects(string $dir): array
    {
        $astLocator = (new BetterReflection())->astLocator();
        $directoriesSourceLocator = new DirectoriesSourceLocator([$dir], $astLocator);
        $reflector = new DefaultReflector($directoriesSourceLocator);
        $classes = [];

        foreach($reflector->reflectAllClasses() as $class) {
            if (count($class->getAttributesByName(DaggerObject::class))) {
                $classes[] = $class->getName();
            }
        }

        return $classes;
    }

    private function hasParentName(string $parentName): bool
    {
        return $parentName === 'null';
    }
}
