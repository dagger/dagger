<?php

namespace Dagger\tests\Unit\Service;

use Dagger\Service\FindsDaggerObjects;
use Dagger\Tests\Unit\Fixture\DaggerObjectWithDaggerFunctions;
use Dagger\Tests\Unit\Fixture\NoDaggerFunctions;
use Dagger\ValueObject\DaggerObject;
use Generator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[CoversClass(FindsDaggerObjects::class)]
class FindsDaggerObjectsTest extends TestCase
{
    /** @param DaggerObject[] $expected */
    #[Test, DataProvider('provideDirectoriesToSearch')]
    public function itFindsDaggerObjects(array $expected, string $dir): void {
        $actual = (new FindsDaggerObjects())($dir);

        self::assertEquals($expected, $actual);
    }

    /** @return Generator<array{ 0: DaggerObject[], 1: string}> */
    public static function provideDirectoriesToSearch(): Generator
    {
        yield 'test fixtures' => [
            [
                NoDaggerFunctions::getValueObjectEquivalent(),
                DaggerObjectWithDaggerFunctions::getValueObjectEquivalent(),

            ],
            __DIR__ . '/../Fixture',
        ];
    }
}
