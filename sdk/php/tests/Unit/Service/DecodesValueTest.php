<?php

namespace Dagger\Tests\Unit\Service;

use Dagger\Client;
use Dagger\Service\DecodesValue;
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
    #[DataProvider('provideScalarValues')]
    public function itDecodesScalarValues(
        mixed $expected,
        string $value,
        string $type,
    ): void {
        $sut = new DecodesValue(self::createStub(Client::class));

        $actual = $sut($value, new Type($type));

        self::assertSame($expected, $actual);
    }

    /**
     * @return \Generator<array{
     *     0: mixed,
     *     1: string,
     *     2: string,
     * }>
     */
    public static function provideScalarValues(): Generator
    {
        yield '(bool) true' => [true, 'true', 'bool'];

        yield '(bool) false' => [false, 'false', 'bool'];

        yield 'int' => [418, '418', 'int'];

        yield 'null' => [null, 'null', 'null'];

        yield 'string' => ['expected', '"expected"', 'string'];

        yield 'void' => [null, '', 'void'];
    }
}
