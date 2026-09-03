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
    public function itResolvesIdHandleTypes(
        string $name,
        ?string $expectedType,
        bool $supportsIdHandles,
        ?string $expected,
    ): void {
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

        self::assertSame($expected, $field->idHandleType($supportsIdHandles));
        self::assertSame($expected !== null, $field->isConvertID($supportsIdHandles));
    }

    public static function idHandleFields(): iterable
    {
        yield 'the id field is never a handle' => ['id', 'LLM', true, null];
        yield 'the parent\'s own ID has always been loaded' => ['sync', 'LLM', false, 'LLM'];
        yield 'another type\'s ID stays raw before the cutover' => ['spawn', 'Agent', false, null];
        yield 'another type\'s ID is loaded from the cutover on' => ['spawn', 'Agent', true, 'Agent'];
        yield 'a bare ID is never a handle' => ['opaque', null, true, null];
    }
}
