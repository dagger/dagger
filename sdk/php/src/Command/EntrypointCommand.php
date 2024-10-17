<?php

declare(strict_types=1);

namespace Dagger\Command;

use Dagger;
use Dagger\Service\FindsDaggerObjects;
use Dagger\Service\FindsSrcDirectory;
use Dagger\Service\NormalizesClassName;
use Dagger\Service\Serialisation;
use Dagger\TypeDef;
use Dagger\TypeDefKind;
use Dagger\ValueObject\DaggerFunction;
use Dagger\ValueObject\ListOfType;
use Dagger\ValueObject\Type;
use Dagger\ValueObject\TypeHint;
use GraphQL\Exception\QueryError;
use ReflectionMethod;
use RuntimeException;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\ConsoleOutputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;

use function Dagger\dag;

#[AsCommand('dagger:entrypoint')]
class EntrypointCommand extends Command
{
    private Serialisation\Serialiser $serialiser;

    protected function execute(
        InputInterface $input,
        OutputInterface $output
    ): int {
        $functionCall = dag()->currentFunctionCall();

        try {
            return $functionCall->parentName() === '' ?
                $this->registerModule($functionCall) :
                $this->callFunctionOnParent($output, $functionCall);
        } catch (\Throwable $t) {
            $this->outputErrorInformation($input, $output, $t);

            return Command::FAILURE;
        }
    }

    private function registerModule(Dagger\FunctionCall $functionCall): int
    {
        $daggerModule = dag()->module();

        $src = (new FindsSrcDirectory())();
        $daggerObjects = (new FindsDaggerObjects())($src);

        foreach ($daggerObjects as $daggerObject) {
            if ($daggerObject instanceof Dagger\ValueObject\DaggerEnum) {
                $enumTypeDef = dag()
                    ->typeDef()
                    ->withEnum(
                        NormalizesClassName::trimLeadingNamespace($daggerObject->getName()),
                        $daggerObject->getDescription(),
                    );

                foreach ($daggerObject->getCases() as $case => $description) {
                    $enumTypeDef = $enumTypeDef
                        ->withEnumValue($case, $description);
                }

                $daggerModule = $daggerModule->withEnum($enumTypeDef);
                continue;
            }

            $objectTypeDef = dag()->typeDef()->withObject(
                NormalizesClassName::trimLeadingNamespace($daggerObject->getName()),
                $daggerObject->getDescription(),
            );

            foreach ($daggerObject->daggerFunctions as $daggerFunction) {
                $func = dag()->function(
                    $daggerFunction->name,
                    $this->getTypeDef($daggerFunction->returnType),
                );

                if ($daggerFunction->description !== null) {
                    $func = $func->withDescription($daggerFunction->description);
                }

                foreach ($daggerFunction->arguments as $argument) {
                    $func = $func->withArg(
                        name: $argument->name,
                        typeDef: $this
                            ->getTypeDef($argument->type)
                            ->withOptional($argument->type->nullable),
                        description: $argument->description,
                        defaultValue: $argument->default,
                        defaultPath: $argument->defaultPath,
                        ignore: $argument->ignore,
                    );
                }

                $objectTypeDef = $daggerFunction->isConstructor() ?
                    $objectTypeDef->withConstructor($func) :
                    $objectTypeDef->withFunction($func);
            }

            $daggerModule = $daggerModule->withObject($objectTypeDef);
        }

        $functionCall->returnValue(new Dagger\Json(json_encode(
            (string) $daggerModule->id()
        )));

        return Command::SUCCESS;
    }

    private function callFunctionOnParent(
        OutputInterface $output,
        Dagger\FunctionCall $functionCall,
    ): int {
        $errorOutput = $output instanceof ConsoleOutputInterface ?
            $output->getErrorOutput() :
            $output;

        $parentName = sprintf('DaggerModule\\%s', $functionCall->parentName());
        $functionName = $functionCall->name();

        $args = $this->formatArguments(
            $parentName,
            $functionName,
            json_decode(json_encode($functionCall->inputArgs()), true)
        );

        try {
            if ($functionName !== '') {
                $class = $this->getSerialiser()->deserialise(
                    (string) $functionCall->parent(),
                    new Type($parentName),
                );
                $result = ($class)->$functionName(...$args);
            } else {
                $result = new $parentName(...$args);
            }
        } catch (QueryError $e) {
            if (!isset($e->getErrorDetails()['extensions'])) {
                throw $e;
            }

            $errorOutput->writeln($e->getMessage());
            $output->writeln($e->getErrorDetails()['extensions']['stdout'] ?? '');
            $errorOutput->writeln($e->getErrorDetails()['extensions']['stderr'] ?? '');

            return $e->getErrorDetails()['extensions']['exitCode'] ??
                Command::FAILURE;
        }

        $result = $this->getSerialiser()->serialise($result);

        $functionCall->returnValue(new Dagger\Json($result));

        return Command::SUCCESS;
    }

    private function getTypeDef(TypeHint $type): TypeDef
    {
        $typeDef = dag()->typeDef()->withOptional($type->isNullable());

        if ($type instanceof ListOfType) {
            return $typeDef->withListOf($this->getTypeDef($type->subtype));
        }

        switch ($type->getTypeDefKind()) {
            case TypeDefKind::BOOLEAN_KIND:
            case TypeDefKind::INTEGER_KIND:
            case TypeDefKind::STRING_KIND:
            case TypeDefKind::VOID_KIND:
                return $typeDef->withKind($type->getTypeDefKind());
            case TypeDefKind::SCALAR_KIND:
                return $typeDef->withScalar(
                    NormalizesClassName::shorten($type->name)
                );
            case TypeDefKind::ENUM_KIND:
                return $typeDef->withEnum(
                    NormalizesClassName::shorten($type->name)
                );
            case TypeDefKind::LIST_KIND:
                return $typeDef->withListOf($this->getTypeDef($type->subtype));
            case TypeDefKind::INTERFACE_KIND:
                throw new RuntimeException(sprintf(
                    'Currently cannot handle custom interfaces: %s',
                    $type->getName(),
                ));
            case TypeDefKind::OBJECT_KIND:
                return $typeDef->withObject($type->isIdable() ?
                    NormalizesClassName::shorten($type->name) :
                    NormalizesClassName::trimLeadingNamespace($type->name));
            default:
                throw new RuntimeException("No support exists for {$type->getName()}");
        }
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
        if ($functionName === '') {
            $functionName = '__construct';
        }

        $daggerFunction = DaggerFunction::fromReflection(
            new ReflectionMethod($className, $functionName)
        );

        $result = [];

        foreach ($daggerFunction->arguments as $parameter) {
            $type = $parameter->type;
            foreach ($arguments as ['Name' => $name, 'Value' => $value]) {
                if ($parameter->name === $name) {
                    $result[$parameter->name] = $this->getSerialiser()
                        ->deserialise($value, $type);
                    continue 2;
                }
            }
        }

        return $result;
    }

    private function getSerialiser(): Serialisation\Serialiser
    {
        if (!isset($this->serialiser)) {
            $this->serialiser = new Serialisation\Serialiser(
                [
                    new Serialisation\EnumSubscriber(),
                    new Serialisation\AbstractScalarSubscriber(),
                    new Serialisation\IdableSubscriber(),
                ],
                [
                    new Serialisation\EnumHandler(),
                    new Serialisation\AbstractScalarHandler(),
                    new Serialisation\IdableHandler(dag()),
                ],
            );
        }

        return $this->serialiser;
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

    private function registerEnum(Type $type, mixed $typeDef, mixed $daggerModule): array
    {
        foreach ($type->getEnumCases() as $case) {
            $typeDef = $typeDef->withEnumValue($case->name);
        }

        $daggerModule = $daggerModule->withEnum($typeDef);
        return [$typeDef, $daggerModule];
    }
}
