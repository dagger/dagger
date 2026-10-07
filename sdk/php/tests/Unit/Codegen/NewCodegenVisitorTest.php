<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Codegen;

use Dagger\Codegen\Introspection\IntrospectionType;
use Dagger\Codegen\Introspection\NewCodegenVisitor;
use Dagger\Codegen\Naming\Identifiers;
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
    public function itNamesMethodsAndArgsFromWords(): void
    {
        $class = $this->generate(self::objectType(), self::identifiers());

        self::assertTrue($class->hasMethod('filterURI'));
        $method = $class->getMethod('filterURI');
        self::assertSame('filterURI', $method->getName());
        self::assertSame(['pushURL', 'callID'], array_keys($method->getParameters()));

        // the schema names still go over the wire
        $body = $method->getBody();
        self::assertStringContainsString("new \\Dagger\\Client\\QueryBuilder('filterUri')", $body);
        self::assertStringContainsString("->setArgument('pushUrl', \$pushURL)", $body);
        self::assertStringContainsString("if (null !== \$callID) {", $body);
        self::assertStringContainsString("->setArgument('callId', \$callID)", $body);
        self::assertStringContainsString("queryLeaf(\$leafQueryBuilder, 'filterUri')", $body);

        // a case-only rename needs no forwarder: PHP method names are case-insensitive
        self::assertSame(['filterURI', 'fooBar', 'foo_bar'], array_keys($class->getMethods()));
    }

    #[Test]
    public function itForwardsMethodsRenamedBeyondCase(): void
    {
        $class = $this->generate(self::objectType(), self::identifiers());

        self::assertSame('fooBar', $class->getMethod('fooBar')->getName());
        $legacy = $class->getMethods()['foo_bar'];
        self::assertStringContainsString('@deprecated Use fooBar() instead.', (string)$legacy->getComment());
        self::assertSame('return $this->fooBar();', $legacy->getBody());
    }

    #[Test]
    public function itKeepsSchemaNamesWithoutWords(): void
    {
        foreach ([null, Identifiers::fromArray([])] as $identifiers) {
            $class = $this->generate(self::objectType(), $identifiers);

            $method = $class->getMethods()['filterUri'];
            self::assertSame(['pushUrl', 'callId'], array_keys($method->getParameters()));
            self::assertStringContainsString("->setArgument('pushUrl', \$pushUrl)", $method->getBody());
            self::assertSame(['filterUri', 'foo_bar'], array_keys($class->getMethods()));
        }
    }

    #[Test]
    public function itNamesEnumCasesFromWords(): void
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

        $enum = $this->generate($type, self::identifiers());
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

    private function generate(
        IntrospectionType $type,
        ?Identifiers $identifiers = null,
    ): ClassType|EnumType|InterfaceType {
        $visitor = $this->getMockBuilder(NewCodegenVisitor::class)
            ->setConstructorArgs(['unused', true, [], $identifiers])
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
                    'name' => 'filterUri',
                    'type' => ['kind' => 'NON_NULL', 'ofType' => $string],
                    'args' => [
                        ['name' => 'pushUrl', 'type' => ['kind' => 'NON_NULL', 'ofType' => $string]],
                        ['name' => 'callId', 'type' => $string],
                    ],
                ],
                [
                    'name' => 'foo_bar',
                    'type' => ['kind' => 'NON_NULL', 'ofType' => $string],
                ],
            ],
        ]);
    }

    private static function identifiers(): Identifiers
    {
        $word = static fn(string $text) => [
            'kind' => 'WORD', 'text' => $text, 'suffix' => '', 'capitalized' => ucfirst($text),
        ];
        $acronym = static fn(string $text) => [
            'kind' => 'ACRONYM', 'text' => $text, 'suffix' => '', 'capitalized' => ucfirst(strtolower($text)),
        ];

        return Identifiers::fromArray([
            'Artifacts' => [$word('artifacts')],
            'filterUri' => [$word('filter'), $acronym('URI')],
            'pushUrl' => [$word('push'), $acronym('URL')],
            'callId' => [$word('call'), $acronym('ID')],
            'foo_bar' => [$word('foo'), $word('bar')],
            'Compression' => [$word('compression')],
            'Gzip' => [$word('gzip')],
            'GZIP' => [$acronym('GZIP')],
            'PerSession' => [$word('per'), $word('session')],
            'Default' => [$word('default')],
        ]);
    }
}
