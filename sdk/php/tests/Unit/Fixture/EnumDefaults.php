<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture;

use Dagger\Attribute\ListOfType;

/**
 * Deliberately not a DaggerObject: FindsDaggerObjects must ignore it. It exists
 * only to give Argument::fromReflection parameters with enum default values.
 */
class EnumDefaults
{
    public function scalar(
        StringBackedEnum $value = StringBackedEnum::Bar,
    ): void {
    }

    public function nullable(
        ?StringBackedEnum $value = null,
    ): void {
    }

    public function pure(
        PureEnum $value = PureEnum::High,
    ): void {
    }

    public function listOfEnums(
        #[ListOfType(StringBackedEnum::class)]
        array $value = [StringBackedEnum::Foo, StringBackedEnum::Baz],
    ): void {
    }
}
