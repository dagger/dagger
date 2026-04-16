<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Dagger\Exception\UnsupportedType;
use Dagger\Tests\Unit\Fixture\Enums\Priority;
use Dagger\Tests\Unit\Fixture\Enums\PureEnum;
use Dagger\Tests\Unit\Fixture\Enums\Status;
use Dagger\ValueObject\DaggerEnum;
use Dagger\ValueObject\DaggerEnumCase;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionEnum;

#[Group('unit')]
#[CoversClass(DaggerEnum::class)]
#[CoversClass(DaggerEnumCase::class)]
class DaggerEnumTest extends TestCase
{
    #[Test]
    public function itBuildsFromStringBackedEnum(): void
    {
        $sut = DaggerEnum::fromReflection(new ReflectionEnum(Status::class));

        self::assertSame(Status::class, $sut->name);
        self::assertSame('The status of a task', $sut->description);
        self::assertCount(3, $sut->cases);

        self::assertSame('Active', $sut->cases[0]->name);
        self::assertSame('active', $sut->cases[0]->value);
        self::assertSame('Task is active', $sut->cases[0]->description);

        self::assertSame('Inactive', $sut->cases[1]->name);
        self::assertSame('inactive', $sut->cases[1]->value);
        self::assertSame('Task is inactive', $sut->cases[1]->description);

        self::assertSame('Pending', $sut->cases[2]->name);
        self::assertSame('pending', $sut->cases[2]->value);
        self::assertSame('', $sut->cases[2]->description);
    }

    #[Test]
    public function itBuildsFromIntBackedEnum(): void
    {
        $sut = DaggerEnum::fromReflection(new ReflectionEnum(Priority::class));

        self::assertSame(Priority::class, $sut->name);
        self::assertSame('Priority level', $sut->description);
        self::assertCount(3, $sut->cases);

        self::assertSame('Low', $sut->cases[0]->name);
        self::assertSame('1', $sut->cases[0]->value);
        self::assertSame('Low priority', $sut->cases[0]->description);

        self::assertSame('Medium', $sut->cases[1]->name);
        self::assertSame('2', $sut->cases[1]->value);

        self::assertSame('High', $sut->cases[2]->name);
        self::assertSame('3', $sut->cases[2]->value);
    }

    #[Test]
    public function itRejectsPureEnums(): void
    {
        self::expectException(UnsupportedType::class);
        self::expectExceptionMessageMatches('/backed enum/');

        DaggerEnum::fromReflection(new ReflectionEnum(PureEnum::class));
    }
}
