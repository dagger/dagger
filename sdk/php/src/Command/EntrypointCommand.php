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
use GraphQL\Exception\QueryError;
use GraphQL\Results;
use GuzzleHttp\Exception\ClientException;
use ReflectionMethod;
use RuntimeException;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\ConsoleOutputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Throwable;

#[AsCommand('dagger:entrypoint')]
class EntrypointCommand extends Command
{
    private Dagger\Client $dag;

    public function __construct()
    {
        parent::__construct();
        $this->dag = Dagger\Dagger::connect();
    }

    protected function execute(
        InputInterface $input,
        OutputInterface $output
    ): int {
        $functionCall = $this->dag->currentFunctionCall();
        $parentName = $functionCall->parentName();

        try {
                return $parentName === '' ?
                    $this->registerModule($functionCall) :
                    $this->callFunctionOnParent(
                        $output,
                        $functionCall,
                        $parentName,
                    );
        } catch (\Throwable $t) {
            $this->outputErrorInformation($input, $output, $t);

            return Command::FAILURE;
        }
    }

    private function registerModule(Dagger\FunctionCall $functionCall): int
    {
        $daggerModule = $this->dag->module();

        $src = (new FindsSrcDirectory())();
        $daggerObjects = (new FindsDaggerObjects())($src);

        foreach ($daggerObjects as $daggerObject) {
            $objectTypeDef = $this->dag
                ->typeDef()
                ->withObject($this->normalizeClassname($daggerObject->name));

            foreach ($daggerObject->daggerFunctions as $daggerFunction) {
                $func = $this->dag->function(
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

        return Command::SUCCESS;
    }

    private function callFunctionOnParent(
        OutputInterface $output,
        Dagger\FunctionCall $functionCall,
        string $parentName
    ): int {
        $errorOutput = $output instanceof ConsoleOutputInterface ?
            $output->getErrorOutput() :
            $output;

        $className = "DaggerModule\\$parentName";
        $functionName = $functionCall->name();
        $class = new $className();
        $class->client = $this->dag;

        $args = $this->formatArguments(
            $className,
            $functionName,
            json_decode(json_encode($functionCall->inputArgs()), true)
        );

        try {
            $result = ($class)->$functionName(...$args);
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

        if ($result instanceof Dagger\Client\IdAble) {
            $result = (string) $result->id();
        }

        $functionCall->returnValue(new Dagger\Json(json_encode($result)));

        return Command::SUCCESS;
    }

    private function getTypeDef(Type $type): TypeDef
    {
        $typeDef = $this->dag->typeDef();
        /**
         * @TODO Support arrays:
         * - Create additional attribute to define the array subtype
         * See: https://github.com/dagger/dagger/blob/main/sdk/typescript/introspector/scanner/utils.ts#L95-L117
         */

        switch ($type->typeDefKind) {
            case TypeDefKind::BOOLEAN_KIND:
            case TypeDefKind::INTEGER_KIND:
            case TypeDefKind::STRING_KIND:
            case TypeDefKind::VOID_KIND:
                return $typeDef->withKind($type->typeDefKind);
            case TypeDefKind::LIST_KIND:
                throw new RuntimeException('Currently cannot handle arrays');
            case TypeDefKind::INTERFACE_KIND:
                throw new RuntimeException(sprintf(
                    'Currently cannot handle custom interfaces: %s',
                    $type->name
                ));
            case TypeDefKind::OBJECT_KIND:
                if ($type->isIdable()) {
                    return $typeDef->withObject($type->getShortName());
                }

                throw new RuntimeException(sprintf(
                    'Currently cannot handle custom classes: %s',
                    $type->name
                ));
            default:
                throw new RuntimeException("No support exists for $type->name");
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
        $decodesValue = new DecodesValue($this->dag);
        foreach ($parameters as $parameter) {
            $type = new Type($parameter->getType()->getName());

            foreach ($arguments as $argument) {
                if ($parameter->name === $argument['Name']) {
                    $result[$parameter->name] = $decodesValue(
                        $argument['Value'],
                        $type
                    );
                    continue 2;
                }
            }
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
