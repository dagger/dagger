<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Service;

use Dagger\Exception\UnsupportedType;
use Dagger\Service\FindsDaggerEnums;
use Dagger\Service\FindsDaggerObjects;
use Dagger\Tests\Unit\Fixture\Enums\ObjectUsingEnum;
use Dagger\Tests\Unit\Fixture\Enums\Priority;
use Dagger\Tests\Unit\Fixture\Enums\PureEnum;
use Dagger\Tests\Unit\Fixture\Enums\Status;
use Dagger\ValueObject;
use Dagger\ValueObject\DaggerEnum;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(FindsDaggerEnums::class)]
class FindsDaggerEnumsTest extends TestCase
{
    private string $fixtureDir;

    protected function setUp(): void
    {
        $this->fixtureDir = __DIR__ . '/../Fixture/Enums';
    }

    #[Test]
    public function itFindsEnumsReferencedInDaggerObjectMethods(): void
    {
        $daggerObjects = (new FindsDaggerObjects())($this->fixtureDir);

        $result = (new FindsDaggerEnums())($daggerObjects);

        $fqns = array_map(fn(DaggerEnum $e) => $e->name, $result);

        self::assertContains(Status::class, $fqns);
        self::assertContains(Priority::class, $fqns);
    }

    #[Test]
    public function itDeduplicatesEnumsReferencedMultipleTimes(): void
    {
        $daggerObjects = (new FindsDaggerObjects())($this->fixtureDir);

        $result = (new FindsDaggerEnums())($daggerObjects);

        $statusOccurrences = array_filter(
            $result,
            fn(DaggerEnum $e) => $e->name === Status::class,
        );

        self::assertCount(1, $statusOccurrences);
    }

    #[Test]
    public function itThrowsOnPureEnum(): void
    {
        $pureEnumType = new ValueObject\Type(PureEnum::class);
        $daggerObjects = [
            new ValueObject\DaggerObject(
                name: 'Fake',
                description: '',
                fields: [],
                functions: [
                    new ValueObject\DaggerFunction(
                        name: 'fake',
                        description: null,
                        arguments: [new ValueObject\Argument('input', '', $pureEnumType)],
                        returnType: $pureEnumType,
                    ),
                ],
            ),
        ];

        $this->expectException(UnsupportedType::class);
        $this->expectExceptionMessageMatches('/backed enum/');

        (new FindsDaggerEnums())($daggerObjects);
    }

    #[Test]
    public function itFindsEnumsFromManuallyBuiltDaggerObjects(): void
    {
        // FindsDaggerEnums operates on DaggerObject value objects, not on PHP
        // attributes. The referenced enum class itself needs no #[DaggerObject]
        // annotation — only the type graph matters.
        $statusType = new ValueObject\Type(Status::class);
        $daggerObjects = [
            new ValueObject\DaggerObject(
                name: 'AnyClass',
                description: '',
                fields: [],
                functions: [
                    new ValueObject\DaggerFunction(
                        name: 'handle',
                        description: null,
                        arguments: [new ValueObject\Argument('s', '', $statusType)],
                        returnType: $statusType,
                    ),
                ],
            ),
        ];

        $result = (new FindsDaggerEnums())($daggerObjects);

        $fqns = array_map(fn(DaggerEnum $e) => $e->name, $result);
        self::assertContains(Status::class, $fqns);
    }

    #[Test]
    public function itReturnsEmptyArrayWhenNoEnumsAreReferenced(): void
    {
        $result = (new FindsDaggerEnums())([]);

        self::assertSame([], $result);
    }

    #[Test]
    public function itReturnsCorrectEnumMetadata(): void
    {
        $daggerObjects = (new FindsDaggerObjects())($this->fixtureDir);
        $result = (new FindsDaggerEnums())($daggerObjects);

        $statusEnum = current(array_filter(
            $result,
            fn(DaggerEnum $e) => $e->name === Status::class,
        ));

        self::assertInstanceOf(DaggerEnum::class, $statusEnum);
        self::assertSame('The status of a task', $statusEnum->description);
        self::assertCount(3, $statusEnum->cases);
        self::assertSame('Active', $statusEnum->cases[0]->name);
        self::assertSame('active', $statusEnum->cases[0]->value);
        self::assertSame('Task is active', $statusEnum->cases[0]->description);
    }
}
