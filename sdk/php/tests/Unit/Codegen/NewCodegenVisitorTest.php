<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Codegen;

use Dagger\Attribute\GraphQLType;
use Dagger\Codegen\Introspection\IntrospectionType;
use Dagger\Codegen\Introspection\NewCodegenVisitor;
use Dagger\Codegen\Naming\FormattedNames;
use Nette\PhpGenerator\ClassType;
use Nette\PhpGenerator\EnumCase;
use Nette\PhpGenerator\EnumType;
use Nette\PhpGenerator\InterfaceType;
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

    #[Test]
    public function itNamesMethodsAndArgsFromFormattedNames(): void
    {
        $class = $this->generate(self::objectType(), self::names());

        self::assertTrue($class->hasMethod('withGpu'));
        $method = $class->getMethod('withGpu');
        self::assertSame('withGpu', $method->getName());
        self::assertSame(['insecureSkipTlsVerify', 'callId'], array_keys($method->getParameters()));

        // the schema names still go over the wire
        $body = $method->getBody();
        self::assertStringContainsString("new \\Dagger\\Client\\QueryBuilder('withGPU')", $body);
        self::assertStringContainsString(
            "->setArgument('insecureSkipTLSVerify', \$insecureSkipTlsVerify)",
            $body,
        );
        self::assertStringContainsString("if (null !== \$callId) {", $body);
        self::assertStringContainsString("->setArgument('callID', \$callId)", $body);
        self::assertStringContainsString("queryLeaf(\$leafQueryBuilder, 'withGPU')", $body);

        // a case-only rename needs no forwarder: PHP method names are case-insensitive
        self::assertSame(['withGpu', 'fooBar', 'foo_bar'], array_keys($class->getMethods()));
    }

    #[Test]
    public function itForwardsMethodsRenamedBeyondCase(): void
    {
        $class = $this->generate(self::objectType(), self::names());

        self::assertSame('fooBar', $class->getMethod('fooBar')->getName());
        $legacy = $class->getMethods()['foo_bar'];
        self::assertStringContainsString('@deprecated Use fooBar() instead.', (string)$legacy->getComment());
        self::assertSame('return $this->fooBar();', $legacy->getBody());
    }

    #[Test]
    public function itKeepsSchemaNamesWithoutFormattedNames(): void
    {
        // no names at all, no names in the format, or no entry for the name
        $partial = new FormattedNames([
            'CAMEL:UPPERCASE' => ['withGPU' => 'withGPU', 'callID' => 'callID'],
            'CAMEL:CAPITALIZED' => ['Artifacts' => 'artifacts'],
        ]);
        foreach ([null, new FormattedNames(), $partial] as $names) {
            $class = $this->generate(self::objectType(), $names);

            $method = $class->getMethods()['withGPU'];
            self::assertSame(['insecureSkipTLSVerify', 'callID'], array_keys($method->getParameters()));
            self::assertStringContainsString(
                "->setArgument('insecureSkipTLSVerify', \$insecureSkipTLSVerify)",
                $method->getBody(),
            );
            self::assertSame(['withGPU', 'foo_bar'], array_keys($class->getMethods()));
        }
    }

    #[Test]
    public function itNamesEnumCasesFromFormattedNames(): void
    {
        $type = IntrospectionType::fromArray([
            'kind' => 'ENUM',
            'name' => 'Compression',
            'enumValues' => [
                ['name' => 'Gzip'],
                ['name' => 'PerSession'],
                ['name' => 'GZIP'],
                ['name' => 'Default'],
            ],
        ]);

        $enum = $this->generate($type, self::names());
        self::assertInstanceOf(EnumType::class, $enum);

        $cases = array_map(
            static fn(EnumCase $case) => $case->getValue(),
            $enum->getCases(),
        );
        self::assertSame([
            // collides with GZIP: keeps its schema name
            'Gzip' => 'Gzip',
            'PER_SESSION' => 'PerSession',
            'GZIP' => 'GZIP',
            'DEFAULT' => 'Default',
        ], $cases);

        $constants = $enum->getConstants();
        self::assertSame(['PerSession', 'Default'], array_keys($constants));
        self::assertSame('self::PER_SESSION', (string)$constants['PerSession']->getValue());
        self::assertStringContainsString('@deprecated', (string)$constants['PerSession']->getComment());

        $legacy = $this->generate($type);
        self::assertInstanceOf(EnumType::class, $legacy);
        self::assertSame(['Gzip', 'PerSession', 'GZIP', 'Default'], array_keys($legacy->getCases()));
        self::assertSame([], $legacy->getConstants());
    }

    #[Test]
    public function itNamesGraphQLTypesOfRenamedClasses(): void
    {
        $jsonValue = IntrospectionType::fromArray(['kind' => 'OBJECT', 'name' => 'JSONValue', 'fields' => []]);
        $artifacts = IntrospectionType::fromArray(['kind' => 'OBJECT', 'name' => 'Artifacts', 'fields' => []]);
        $names = new FormattedNames();

        $class = $this->generate($jsonValue, $names);
        self::assertSame('JsonValue', $class->getName());
        $attributes = $class->getAttributes();
        self::assertCount(1, $attributes);
        self::assertSame(GraphQLType::class, $attributes[0]->getName());
        self::assertSame(['JSONValue'], $attributes[0]->getArguments());

        // named like the GraphQL type: nothing to say
        self::assertSame([], $this->generate($artifacts, $names)->getAttributes());

        // without formatted names, the generated code stays as it was
        self::assertSame([], $this->generate($jsonValue)->getAttributes());
    }

    private function generate(
        IntrospectionType $type,
        ?FormattedNames $names = null,
    ): ClassType|EnumType|InterfaceType {
        $visitor = $this->getMockBuilder(NewCodegenVisitor::class)
            ->setConstructorArgs(['unused', true, [], $names])
            ->onlyMethods(['write'])
            ->getMock();
        $written = null;
        $visitor->expects(self::once())->method('write')->with(self::callback(
            static function (ClassType|EnumType|InterfaceType $class) use (&$written): bool {
                $written = $class;
                return true;
            },
        ));

        match ($type->kind) {
            'ENUM' => $visitor->visitEnum($type),
            default => $visitor->visitObject($type),
        };
        self::assertNotNull($written);

        return $written;
    }

    private static function objectType(): IntrospectionType
    {
        $string = ['kind' => 'SCALAR', 'name' => 'String'];
        return IntrospectionType::fromArray([
            'kind' => 'OBJECT',
            'name' => 'Artifacts',
            'fields' => [
                [
                    'name' => 'withGPU',
                    'type' => ['kind' => 'NON_NULL', 'ofType' => $string],
                    'args' => [
                        ['name' => 'insecureSkipTLSVerify', 'type' => ['kind' => 'NON_NULL', 'ofType' => $string]],
                        ['name' => 'callID', 'type' => $string],
                    ],
                ],
                [
                    'name' => 'foo_bar',
                    'type' => ['kind' => 'NON_NULL', 'ofType' => $string],
                ],
            ],
        ]);
    }

    private static function names(): FormattedNames
    {
        return new FormattedNames([
            'CAMEL:CAPITALIZED' => [
                'Artifacts' => 'artifacts',
                'withGPU' => 'withGpu',
                'insecureSkipTLSVerify' => 'insecureSkipTlsVerify',
                'callID' => 'callId',
                'foo_bar' => 'fooBar',
            ],
            'SCREAMING_SNAKE:UPPERCASE' => [
                'Compression' => 'COMPRESSION',
                'Gzip' => 'GZIP',
                'GZIP' => 'GZIP',
                'PerSession' => 'PER_SESSION',
                'Default' => 'DEFAULT',
            ],
        ]);
    }
}
