<?php

namespace Dagger\tests\Unit\Service;

use Dagger\Client;
use Dagger\Service\DecodesValue;
use Generator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

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
    public static function provideScalarValues(): Generator
    {
        yield 'string' => ['expected', '"expected"', 'string'];

        yield 'int' => [418, '418', 'int'];

        yield '(bool) true' => [true, 'true', 'bool'];

        yield '(bool) false' => [false, 'false', 'bool'];
    }
}
