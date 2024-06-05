<?php

namespace Dagger\Command;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Client;
use Dagger\Connection;
use Dagger\Container;
use Dagger\Directory;
use Dagger\File;
use Dagger\Json as DaggerJson;
use Dagger\TypeDef;
use GuzzleHttp\Psr7\Response;
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

        $currentFunctionCall = $this->daggerConnection->currentFunctionCall();
        $parentName = $currentFunctionCall->parent()->getValue();

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

        try {
            $daggerModule = $this->daggerConnection->module();

            // Find classes tagged with [DaggerFunction]
            foreach ($classes as $class) {
                $io->info('FOUND CLASS WITH DaggerFunction annotation: ' . $class);
                $reflectedClass = new ReflectionClass($class);

                $typeDef = $this->daggerConnection->typeDef()
                    ->withObject($this->normalizeClassname($reflectedClass->getName()));

                // Loop thru all the functions in this class
                foreach ($reflectedClass->getMethods(ReflectionMethod::IS_PUBLIC) as $method) {
                    $functionAttribute = $this->getDaggerFunctionAttribute($method);
                    if ($functionAttribute === null) {
                        continue;
                    }
                    // We found a method with a DaggerFunction attribute! yay!
                    $io->info('FOUND METHOD with DaggerFunction attribute! yay');

                    $methodName = $method->getName();
                    $io->info('FOUND METHOD: ' . $methodName);

                    $methodReturnType = $method->getReturnType();

                    // Perhaps Dagger mandates a return type, and if we don't find one,
                    // then we flag up an error/notice/exception/warning
                    //@TODO is this check sufficient to ensure a return type?
                    //@TODO when we figure out how to support union/intersection types, we still need a check for no return type
                    if (!($methodReturnType instanceof \ReflectionNamedType)) {
                        throw new \RuntimeException('Cannot handle union/intersection types yet');
                    }

                    $func = $this->daggerConnection->function(
                        $methodName,
                        $this->getTypeDefFromPHPType($methodReturnType)
                    );

                    foreach ($method->getParameters() as $arg) {
                        $argType = $arg->getType();
                        //@TODO see above notes on arg types
                        if (!($argType instanceof \ReflectionNamedType)) {
                            throw new \RuntimeException('Cannot handle union/intersection types yet');
                        }

                        $func = $func->withArg($arg->getName(), $this->getTypeDefFromPHPType($argType));
                    }

                    $typeDef = $typeDef->withFunction($func);
                }


                $daggerModule = $daggerModule->withObject($typeDef);


                // $reflectionMethod = new ReflectionMethod($reflectedClass->, 'myMethod');
                // // Get the attributes of the method
                // $attributes = $reflectionMethod->getAttributes();
                // foreach ($attributes as $attribute) {
                //     $attributeInstance = $attribute->newInstance();
                //     echo 'Attribute class: ' . $attribute->getName() . PHP_EOL;
                //     echo 'Attribute value: ' . $attributeInstance->value . PHP_EOL;
                // }

            }

            // SUCCESS - WE HAVE DAGGER ID
            $io->info('DAGGER MODULE ID' . substr($daggerModule->id(), 0, 10));
            $result = $daggerModule->id();
            $currentFunctionCall->returnValue(new DaggerJson(json_encode((string) $result)));
        } catch (\Throwable $t) {
            //@TODO tidy this up...
            $io->error($t->getMessage());
            if (method_exists($t, 'getResponse')) {
                /** @var Response $response */
                $response = $t->getResponse();
                $io->error($response->getBody()->getContents());
            }
            $io->error($t->getTraceAsString());
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

    private function getDaggerFunctionAttribute(ReflectionMethod $method): ?DaggerFunction
    {
        $attribute = current($method->getAttributes(DaggerFunction::class)) ?: null;
        return $attribute?->newInstance();
    }

    private function getTypeDefFromPHPType(\ReflectionNamedType $methodReturnType): TypeDef
    {
        // See: https://github.com/dagger/dagger/blob/main/sdk/typescript/introspector/scanner/utils.ts#L95-L117
        //@TODO support descriptions, optional and defaults.
        //@TODO support arrays via additional attribute to define the array subtype
        switch ($methodReturnType->getName()) {
            case 'string':
                return $this->daggerConnection->typeDef()->withKind(TypeDefKind::STRING_KIND);
            case 'int':
                return $this->daggerConnection->typeDef()->withKind(TypeDefKind::INTEGER_KIND);
            case 'bool':
                return $this->daggerConnection->typeDef()->withKind(TypeDefKind::BOOLEAN_KIND);
            case 'float':
            case 'array':
                throw new \RuntimeException('cant support type: ' . $methodReturnType->getName());
            case 'void':
                return $this->daggerConnection->typeDef()->withKind(TypeDefKind::VOID_KIND);
            case Container::class:
                return $this->daggerConnection->typeDef()->withObject('Container');
            case Directory::class:
                return $this->daggerConnection->typeDef()->withObject('Directory');
            case File::class:
                return $this->daggerConnection->typeDef()->withObject('File');
            default:
                if (class_exists($methodReturnType->getName())) {
                    return $this->daggerConnection->typeDef()->withObject($this->normalizeClassname($methodReturnType->getName()));
                }
                if (interface_exists($methodReturnType->getName())) {
                    return $this->daggerConnection->typeDef()->withInterface($this->normalizeClassname($methodReturnType->getName()));
                }

                throw new \RuntimeException('dont know what to do with: ' . $methodReturnType->getName());

        }
    }

    private function normalizeClassname(string $classname): string
    {
        $classname = str_replace('DaggerModule', '', $classname);
        $classname = ltrim($classname, '\\');
        return str_replace('\\', ':', $classname);
    }
}
