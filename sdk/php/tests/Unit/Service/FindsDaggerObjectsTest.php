<?php

namespace Dagger\Tests\Unit\Service;

use Dagger\Service\FindsDaggerObjects;
use Dagger\Tests\Unit\Fixture\DaggerObject\HandlingEnums;
use Dagger\Tests\Unit\Fixture\DaggerObjectWithDaggerFunctions;
use Dagger\Tests\Unit\Fixture\Enum\IntBackedDummy;
use Dagger\Tests\Unit\Fixture\Enum\StringBackedDummy;
use Dagger\Tests\Unit\Fixture\Enum\UnitDummy;
use Dagger\Tests\Unit\Fixture\NoDaggerFunctions;
use Dagger\ValueObject\DaggerObject;
use Generator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(FindsDaggerObjects::class)]
class FindsDaggerObjectsTest extends TestCase
{
    /** @param DaggerObject[] $expected */
    #[Test, DataProvider('provideDirectoriesToSearch')]
    public function itFindsDaggerObjects(array $expected, string $dir): void
    {
        $actual = (new FindsDaggerObjects())($dir);

        self::assertEqualsCanonicalizing($expected, array_values($actual));
    }

    /** @return Generator<array{ 0: DaggerObject[], 1: string}> */
    public static function provideDirectoriesToSearch(): Generator
    {
        yield 'test fixtures' => [
            [
                NoDaggerFunctions::getValueObjectEquivalent(),
                DaggerObjectWithDaggerFunctions::getValueObjectEquivalent(),
                HandlingEnums::getValueObjectEquivalent(),
                StringBackedDummy::getValueObjectEquivalent(),
                IntBackedDummy::getValueObjectEquivalent(),
                UnitDummy::getValueObjectEquivalent(),
            ],
            __DIR__ . '/../Fixture',
        ];
    }
}
