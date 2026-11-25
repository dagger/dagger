<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Codegen;

use Dagger\Client\AbstractClient;
use Dagger\Codegen\CodeWriter;
use Dagger\Codegen\Introspection\IntrospectionType;
use Dagger\Codegen\Introspection\NewCodegenVisitor;
use Dagger\GraphQl\QueryBuilderChain;
use Nette\PhpGenerator\ClassType;
use org\bovigo\vfs\vfsStream;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(NewCodegenVisitor::class)]
class NewCodegenVisitorTest extends TestCase
{
    #[Test]
    #[DataProvider('inputLists')]
    public function itGeneratesArrayParametersForInputLists(array $element, bool $required): void
    {
        $fieldType = ['kind' => 'LIST', 'ofType' => $element];
        if ($required) {
            $fieldType = ['kind' => 'NON_NULL', 'ofType' => $fieldType];
        }
        $type = IntrospectionType::fromArray([
            'kind' => 'INPUT_OBJECT',
            'name' => 'ExampleInput',
            'inputFields' => [['name' => 'content', 'type' => $fieldType]],
        ]);
        $visitor = $this->getMockBuilder(NewCodegenVisitor::class)
            ->setConstructorArgs(['unused'])
            ->onlyMethods(['write'])
            ->getMock();
        $visitor->expects(self::once())->method('write')->with(self::callback(
            static function (ClassType $class) use ($required): bool {
                $parameter = $class->getMethod('__construct')->getParameters()['content'];
                self::assertSame('array', $parameter->getType());
                self::assertSame(!$required, $parameter->isNullable());
                return true;
            },
        ));

        $visitor->visitInput($type);
    }

    #[Test]
    #[DataProvider('falseDefaultArguments')]
    public function itGeneratesQueriesWithTheExpectedFalseArguments(
        string $typeName,
        string $fieldName,
        string $argumentName,
        string $expectedQuery,
    ): void {
        $booleanType = ['kind' => 'SCALAR', 'name' => 'Boolean'];
        $type = IntrospectionType::fromArray([
            'kind' => 'OBJECT',
            'name' => $typeName,
            'fields' => [[
                'name' => $fieldName,
                'type' => $booleanType,
                'args' => [[
                    'name' => $argumentName,
                    'type' => ['kind' => 'NON_NULL', 'ofType' => $booleanType],
                    'defaultValue' => 'false',
                ]],
            ]],
        ]);
        $directory = vfsStream::setup()->url();
        $className = 'Generated' . $typeName . ucfirst($fieldName);
        $writer = new CodeWriter($directory);
        $visitor = $this->getMockBuilder(NewCodegenVisitor::class)
            ->setConstructorArgs([$directory])
            ->onlyMethods(['write'])
            ->getMock();
        $visitor->expects(self::once())->method('write')->willReturnCallback(
            static fn (ClassType $class) => $writer->write($class->setName($className))
        );

        $visitor->visitObject($type);
        require $directory . '/' . $className . '.php';
        $class = CodeWriter::NAMESPACE . '\\' . $className;
        $client = $this->createMock(AbstractClient::class);
        $client->expects(self::once())->method('queryLeaf')->willReturnCallback(
            static function ($query) use ($expectedQuery): bool {
                self::assertSame($expectedQuery, (string) $query);
                return false;
            }
        );

        $object = new $class($client, new QueryBuilderChain());
        $object->$fieldName(false);
    }

    public static function falseDefaultArguments(): iterable
    {
        yield ['Artifact', 'dimensionKeys', 'all', 'query { dimensionKeys }'];
        yield ['Artifacts', 'dimensions', 'all', 'query { dimensions }'];
        yield ['Artifacts', 'filterDimensions', 'exclude', 'query { filterDimensions }'];
        yield ['Container', 'withExec', 'useEntrypoint', 'query { withExec(useEntrypoint: false) }'];
    }

    public static function inputLists(): iterable
    {
        foreach ([false, true] as $required) {
            yield [['kind' => 'SCALAR', 'name' => 'String'], $required];
            yield [['kind' => 'ENUM', 'name' => 'LLMContentBlockKind'], $required];
            yield [[
                'kind' => 'NON_NULL',
                'ofType' => ['kind' => 'INPUT_OBJECT', 'name' => 'LLMContentBlockInput'],
            ], $required];
        }
    }
}
