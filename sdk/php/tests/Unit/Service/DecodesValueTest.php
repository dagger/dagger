<?php

namespace Dagger\Tests\Unit\Service;

use Dagger\Client;
use Dagger\Service\DecodesValue;
use Dagger\Tests\Unit\Fixture\Enums\Priority;
use Dagger\Tests\Unit\Fixture\Enums\Status;
use Dagger\ValueObject\ListOfType;
use Dagger\ValueObject\Type;
use Generator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(DecodesValue::class)]
class DecodesValueTest extends TestCase
{
    #[Test]
    #[DataProvider('provideScalars')]
    #[DataProvider('provideLists')]
    #[DataProvider('provideEnums')]
    public function itDecodesScalarsAndLists(
        mixed $expected,
        string $value,
        ListOfType|Type $type,
    ): void {
        $sut = new DecodesValue(self::createStub(Client::class));

        $actual = $sut($value, $type);

        self::assertSame($expected, $actual);
    }

    /**
     * @return \Generator<array{
     *     0: mixed,
     *     1: string,
     *     2: string,
     * }>
     */
    public static function provideScalars(): Generator
    {
        yield '(bool) true' => [true, 'true', new Type('bool')];

        yield '(bool) false' => [false, 'false', new Type('bool')];

        yield '(int) 418' => [418, '418', new Type('int')];

        yield '(null) null' => [null, 'null', new Type('null')];

        yield '(null) empty string' => [null, 'null', new Type('null')];

        yield '(string) "expected"' => ['expected', '"expected"', new Type('string')];

        yield '(void) null' => [null, 'null', new Type('void')];

        yield '(void) empty string' => [null, '', new Type('void')];
    }

    /**
     * @return \Generator<array{
     *     0: mixed,
     *     1: string,
     *     2: string,
     * }>
     */
    public static function provideLists(): Generator
    {
        yield '[String]' => [
            ['hello', 'world'],
            '["hello","world"]',
            new ListOfType(new Type('string', true), true),
        ];

        yield '[String], passed null' => [
            null,
            'null',
            new ListOfType(new Type('string', true), true),
        ];

        yield '[String], passed empty string' => [
            null,
            '',
            new ListOfType(new Type('string', true), true),
        ];

        yield '[String], passed array of null' => [
            [null, null],
            '[null,null]',
            new ListOfType(new Type('string', true), true),
        ];

        yield '[String], passed array of empty strings' => [
            [null, null],
            '[,]',
            new ListOfType(new Type('string', true), true),
        ];

        yield '[String]!' => [
            ['hello', 'world'],
            '["hello","world"]',
            new ListOfType(new Type('string', true), false),
        ];

        yield '[String]!, passed array of null' => [
            [null, null],
            '[null, null]',
            new ListOfType(new Type('string', true), false),
        ];

        yield '[String!]' => [
            ['hello', 'world'],
            '["hello","world"]',
            new ListOfType(new Type('string', false), true),
        ];

        yield '[String!], passed null' => [
            null,
            '',
            new ListOfType(new Type('string', false), true),
        ];

        yield '[String!]!' => [
            ['hello', 'world'],
            '["hello","world"]',
            new ListOfType(new Type('string', false), false),
        ];

    }

    /**
     * @return \Generator<array{0: mixed, 1: string, 2: Type}>
     */
    public static function provideEnums(): Generator
    {
        // Engine sends back the PHP case name (registered via withEnumValue($case->name))
        yield 'string-backed enum by case name' => [
            Status::Active,
            '"Active"',
            new Type(Status::class),
        ];

        yield 'int-backed enum by case name' => [
            Priority::Medium,
            '"Medium"',
            new Type(Priority::class),
        ];

        // Fallback: engine sends the backing value (built-in Dagger enums or other SDKs)
        yield 'string-backed enum by backing value' => [
            Status::Active,
            '"active"',
            new Type(Status::class),
        ];

        yield 'int-backed enum by backing value' => [
            Priority::Medium,
            '2',
            new Type(Priority::class),
        ];
    }
}
