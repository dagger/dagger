<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Codegen;

use Dagger\Codegen\Introspection\IntrospectionType;
use Dagger\Codegen\Introspection\NewCodegenVisitor;
use Nette\PhpGenerator\ClassType;
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
}
