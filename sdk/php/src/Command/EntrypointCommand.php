<?php

namespace Dagger\Command;

use Dagger\Attribute\DaggerFunction;
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
use Dagger\ScalarTypeDef;
use Dagger\TypeDefKind;
use ReflectionAttribute;
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

        // Find classes tagged with [DaggerFunction]
        foreach($classes as $class) {

            $io->info('FOUND CLASS WITH DaggerFunction annotation: ' . $class);

            $daggerModule = $this->daggerConnection->module();

            // @todo - need to take the RETURN type of the method, and map that to the correct dagger TypeDefKind
            // See: https://github.com/dagger/dagger/blob/main/sdk/typescript/introspector/scanner/utils.ts#L95-L117
            $stringType = $this->daggerConnection
                ->typeDef()
                ->withKind(TypeDefKind::STRING_KIND);
        

            // Loop thru all the functions in this class
            $reflectedClass = new ReflectionClass($class);
            $reflectedMethods = $reflectedClass->getMethods(ReflectionMethod::IS_PUBLIC);
            // $io->info(var_export($reflectedMethods, true));

            foreach($reflectedMethods as $method) {
                $methodName = $method->getName();
                $io->info('FOUND METHOD: ' . $methodName);

                $methodReturnType = $method->getReturnType();

                $func = $this->daggerConnection->function($methodName, $stringType);
                $obj = $this->daggerConnection->typeDef()
                    ->withObject('PaulTestModule')
                    ->withFunction($func);

                $daggerModule = $daggerModule->withObject($obj);

                // Premarurely end the loop here...
                continue;

                $methodAttributes = $method->getAttributes();

                foreach($methodAttributes as $methodAttribute) {
                    if(!$this->hasDaggerFunctionAttribute($methodAttribute)) {
                        continue;
                    }

                    // We found a method with a DaggerFunction attribute! yay!
                    $io->info('FOUND METHOD with DaggerFunction attribute! yay');

                    $methodArgs = $method->getParameters();

                    // Perhaps Dagger mandates a return type, and if we don't find one,
                    // then we flag up an error/notice/exception/warning
                    
                    foreach($methodArgs as $arg) {
                        $argType = $arg->getType()->getName();
                        $argName = $arg->getName();
                        $io->info('METHOD: ' . $method->getName() . ' - ARG: ' . $arg->getName());
                        $io->info('ARG :   ' . $argName . ' - OF TYPE: ' . $argType);
                    }

                    /*$client->module()->withObject(
                        $client->typeDef()->withFunction(
                            $client->function()
                                ->withArg()
                        )
                    );*/

                    // create a ->withFunction entry
                    // Find the args on the function, and do ->withArg() on it
                }
                // $io->info(var_export($methodAttributes, true));
            }

            // SUCCESS - WE HAVE DAGGER ID
            $io->info('DAGGER MODULE ID' . substr($daggerModule->id(), 0, 10));

            // $reflectionMethod = new ReflectionMethod($reflectedClass->, 'myMethod');
            // // Get the attributes of the method
            // $attributes = $reflectionMethod->getAttributes();
            // foreach ($attributes as $attribute) {
            //     $attributeInstance = $attribute->newInstance();
            //     echo 'Attribute class: ' . $attribute->getName() . PHP_EOL;
            //     echo 'Attribute value: ' . $attributeInstance->value . PHP_EOL;
            // }

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
        return $parentName !== 'null';
    }

    private function hasDaggerFunctionAttribute(ReflectionAttribute $attribute): bool
    {
        return $attribute->getName() === DaggerFunction::class;
    }

}
