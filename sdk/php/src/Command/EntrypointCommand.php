<?php

declare(strict_types=1);

namespace Dagger\Command;

use Dagger;
use Dagger\Container;
use Dagger\Directory;
use Dagger\File;
use Dagger\Service\DecodesValue;
use Dagger\Service\FindsDaggerObjects;
use Dagger\Service\FindsSrcDirectory;
use Dagger\TypeDef;
use Dagger\TypeDefKind;
use Dagger\ValueObject\Type;
use ReflectionMethod;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;

#[AsCommand('dagger:entrypoint')]
class EntrypointCommand extends Command
{
    private Dagger\Client $daggerClient;

    public function __construct()
    {
        parent::__construct();
        $this->daggerClient = Dagger\Dagger::connect();
    }

    protected function execute(
        InputInterface $input,
        OutputInterface $output
    ): int {
        $functionCall = $this->daggerClient->currentFunctionCall();
        $parentName = $functionCall->parentName();

        try {
            $parentName === '' ?
                $this->registerModule($functionCall) :
                $this->callFunctionOnParent($functionCall, $parentName);
        } catch (\Throwable $t) {
            $this->outputErrorInformation($input, $output, $t);

            return Command::FAILURE;
        }

        return Command::SUCCESS;
    }

    private function registerModule(Dagger\FunctionCall $functionCall): void
    {
        $daggerModule = $this->daggerClient->module();

        $src = (new FindsSrcDirectory())();
        $daggerObjects = (new FindsDaggerObjects())($src);

        foreach ($daggerObjects as $daggerObject) {
            $objectTypeDef = $this->daggerClient
                ->typeDef()
                ->withObject($this->normalizeClassname($daggerObject->name));

            foreach ($daggerObject->daggerFunctions as $daggerFunction) {
                $func = $this->daggerClient->function(
                    $daggerFunction->name,
                    $this->getTypeDef($daggerFunction->returnType)
                );

                if ($daggerFunction->description !== null) {
                    $func = $func->withDescription($daggerFunction->description);
                }

                foreach ($daggerFunction->arguments as $argument) {
                    $func = $func->withArg(
                        $argument->name,
                        $this->getTypeDef($argument->type),
                        $argument->description,
                        $argument->default
                    );
                }

                $objectTypeDef = $objectTypeDef->withFunction($func);
            }

            $daggerModule = $daggerModule->withObject($objectTypeDef);
        }

        $functionCall->returnValue(new Dagger\Json(json_encode(
            (string) $daggerModule->id()
        )));
    }

    private function callFunctionOnParent(
        Dagger\FunctionCall $functionCall,
        string $parentName
    ): void {
        $className = "DaggerModule\\$parentName";
        $functionName = $functionCall->name();
        $class = new $className();
        $class->client = $this->daggerClient;

        $args = $this->formatArguments(
            $className,
            $functionName,
            json_decode(json_encode($functionCall->inputArgs()), true)
        );

        $result = ($class)->$functionName(...$args);
        if ($result instanceof Dagger\Client\IdAble) {
            $result = (string) $result->id();
        }

        $functionCall->returnValue(new Dagger\Json(json_encode($result)));
    }

    private function getTypeDef(Type $type): TypeDef
    {
        $typeDef = $this->daggerClient->typeDef();
        // See: https://github.com/dagger/dagger/blob/main/sdk/typescript/introspector/scanner/utils.ts#L95-L117
        //@TODO support arrays via additional attribute to define the array subtype
        switch ($type->name) {
            case 'string':
                return $typeDef->withKind(TypeDefKind::STRING_KIND);
            case 'int':
                return $typeDef->withKind(TypeDefKind::INTEGER_KIND);
            case 'bool':
                return $typeDef->withKind(TypeDefKind::BOOLEAN_KIND);
            case 'float':
            case 'array':
            throw new \RuntimeException('cant support type: ' . $type->name);
            case 'void':
                return $typeDef->withKind(TypeDefKind::VOID_KIND);
            case Container::class:
                return $typeDef->withObject('Container');
            case Directory::class:
                return $typeDef->withObject('Directory');
            case File::class:
                return $typeDef->withObject('File');
            default:
                if (class_exists($type->name)) {
                    throw new \RuntimeException(sprintf(
                        'Currently cannot handle custom classes: %s',
                        $type->name
                    ));
                }

                if (interface_exists($type->name)) {
                    throw new \RuntimeException(sprintf(
                        'Currently cannot handle custom interfaces: %s',
                        $type->name
                    ));
                }

                throw new \RuntimeException('dont know what to do with: ' . $type->name);
        }
    }

    private function normalizeClassname(string $classname): string
    {
        $classname = str_replace('DaggerModule', '', $classname);
        $classname = ltrim($classname, '\\');
        return str_replace('\\', ':', $classname);
    }

    /**
     * @param array<array{Name:string,Value:string}> $arguments
     *
     * @return array<string,mixed>
     */
    private function formatArguments(
        string $className,
        string $functionName,
        array $arguments,
    ): array {
        $parameters = (new ReflectionMethod($className, $functionName))
            ->getParameters();

        $result = [];
        $formatsValue = new DecodesValue($this->daggerClient);
        foreach ($parameters as $parameter) {
            foreach ($arguments as $argument) {
                if ($parameter->name === $argument['Name']) {
                    $result[$parameter->name] = $formatsValue(
                        $argument['Value'],
                        $parameter->getType()->getName()
                    );
                    continue 2;
                }
            }
            // todo if no argument matched && default === null,
        }

        return $result;
    }

    private function outputErrorInformation(
        InputInterface $input,
        OutputInterface $output,
        \Throwable $t
    ): void {
        $io = new SymfonyStyle($input, $output);
        $io->error($t->getMessage());
        if (method_exists($t, 'getResponse')) {
            $response = $t->getResponse();
            $io->error($response->getBody()->getContents());
        }
        $io->error($t->getTraceAsString());
    }
}
