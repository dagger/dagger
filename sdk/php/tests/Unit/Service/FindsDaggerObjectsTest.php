<?php

namespace Dagger\Tests\Unit\Service;

use Dagger\Service\FindsDaggerObjects;
use Dagger\Tests\Unit\Fixture;
use Dagger\ValueObject\DaggerEnum;
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
    /** @param list<DaggerEnum|DaggerObject> $expected */
    #[Test, DataProvider('provideDirectoriesToSearch')]
    public function itFindsDaggerObjects(array $expected, string $dir): void
    {
        $actual = (new FindsDaggerObjects())($dir);

        // Discovery order is not guaranteed, and the result mixes value object
        // types, which sort unpredictably. Compare keyed by name instead.
        self::assertEquals(
            self::indexByName($expected),
            self::indexByName($actual),
        );
    }

    /**
     * @param list<DaggerEnum|DaggerObject> $found
     * @return array<string, DaggerEnum|DaggerObject>
     */
    private static function indexByName(array $found): array
    {
        $indexed = [];
        foreach ($found as $one) {
            $indexed[$one->name] = $one;
        }
        ksort($indexed);

        return $indexed;
    }

    /** @return Generator<array{ 0: list<DaggerEnum|DaggerObject>, 1: string}> */
    public static function provideDirectoriesToSearch(): Generator
    {
        yield 'test fixtures' => [
            [
                Fixture\NoDaggerFunctions::getValueObjectEquivalent(),
                Fixture\DaggerObjectUsingEnums::getValueObjectEquivalent(),
                Fixture\DaggerObjectWithDaggerFunctions::getValueObjectEquivalent(),
                Fixture\Module\Field\MyModule::asValueObject(),
                Fixture\StringBackedEnum::asValueObject(),
                Fixture\IntBackedEnum::asValueObject(),
                Fixture\PureEnum::asValueObject(),
            ],
            __DIR__ . '/../Fixture',
        ];
    }
}
