<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Dagger\Exception\UnsupportedType;
use Dagger\ValueObject;
use Dagger\Tests\Unit\Fixture;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionEnum;

#[Group('unit')]
#[CoversClass(ValueObject\DaggerEnum::class)]

class DaggerEnumTest extends TestCase
{
    #[Test]
    #[DataProvider('provideStringBackedEnums')]
    public function itBuildsFromReflection(
        ValueObject\DaggerEnum $expected,
        \ReflectionEnum $reflection,
    ): void {
        $actual = ValueObject\DaggerEnum::fromReflection($reflection);
        self::assertEquals($expected, $actual);
    }

    /**
     * @return \Generator<array{
     *     ValueObject\DaggerEnum,
     *     \ReflectionEnum,
     * }>
     */
    public static function provideStringBackedEnums(): \Generator
    {
        yield 'generic string-backed fixture' => [
            Fixture\StringBackedEnum::asValueObject(),
            new \ReflectionEnum(Fixture\StringBackedEnum::class),
        ];
    }
}
