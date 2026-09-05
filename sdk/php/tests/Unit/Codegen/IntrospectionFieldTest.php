<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Codegen;

use Dagger\Codegen\Introspection\IntrospectionField;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(IntrospectionField::class)]
class IntrospectionFieldTest extends TestCase
{
    #[Test]
    #[DataProvider('idHandleFields')]
    public function itResolvesIdHandleTypes(string $name, ?string $expectedType, ?string $expected): void
    {
        $field = IntrospectionField::fromArray([
            'name' => $name,
            'type' => [
                'kind' => 'NON_NULL',
                'ofType' => ['kind' => 'SCALAR', 'name' => 'ID'],
            ],
            'directives' => $expectedType === null ? [] : [
                ['name' => 'expectedType', 'args' => [['name' => 'name', 'value' => '"' . $expectedType . '"']]],
            ],
        ]);
        $field->parentTypeName = 'LLM';

        self::assertSame($expected, $field->idHandleType());
        self::assertSame($expected !== null, $field->isConvertID());
    }

    public static function idHandleFields(): iterable
    {
        yield 'the id field is never a handle' => ['id', 'LLM', null];
        yield 'the parent\'s own ID loads the parent' => ['sync', 'LLM', 'LLM'];
        yield 'another type\'s ID loads that type' => ['spawn', 'Agent', 'Agent'];
        yield 'a bare ID is never a handle' => ['opaque', null, null];
    }
}
