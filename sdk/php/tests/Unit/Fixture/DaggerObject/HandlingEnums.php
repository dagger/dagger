<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture\DaggerObject;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Json;
use Dagger\NetworkProtocol;
use Dagger\Tests\Unit\Fixture\Enum\StringBackedDummy;
use Dagger\ValueObject;

#[DaggerObject]
final class HandlingEnums
{
    #[DaggerFunction('Test handling of built-in enums')]
    public function requiredNetworkProtocol(
        NetworkProtocol $arg,
    ): NetworkProtocol {
        return $arg;
    }

    #[DaggerFunction('Test handling of defaults for built-in enums')]
    public function defaultNetworkProtocol(
        NetworkProtocol $arg = NetworkProtocol::TCP,
    ): NetworkProtocol {
        return $arg;
    }

    #[DaggerFunction('Test handling of custom string-backed enums')]
    public function requiredStringBackedEnum(
        StringBackedDummy $arg,
    ): StringBackedDummy {
        return $arg;
    }

    #[DaggerFunction('Test handling of defaults for custom string-backed enums')]
    public function defaultStringBackedEnum(
        StringBackedDummy $arg = StringBackedDummy::Hello,
    ): StringBackedDummy {
        return StringBackedDummy::World;
    }

    public static function getValueObjectEquivalent(): ValueObject\DaggerObject
    {
        return new ValueObject\DaggerClass(HandlingEnums::class, '', [
            new ValueObject\DaggerFunction(
                'requiredNetworkProtocol',
                'Test handling of built-in enums',
                [
                    new ValueObject\Argument(
                        'arg',
                        '',
                        new ValueObject\Type(NetworkProtocol::class, false),
                        null,
                    ),
                ],
                new ValueObject\Type(NetworkProtocol::class),
            ),
            new ValueObject\DaggerFunction(
                'defaultNetworkProtocol',
                'Test handling of defaults for built-in enums',
                [
                    new ValueObject\Argument(
                        'arg',
                        '',
                        new ValueObject\Type(NetworkProtocol::class, false),
                        new Json('"TCP"'),
                    ),
                ],
                new ValueObject\Type(NetworkProtocol::class),
            ),
            new ValueObject\DaggerFunction(
                'requiredStringBackedEnum',
                'Test handling of custom string-backed enums',
                [
                    new ValueObject\Argument(
                        'arg',
                        '',
                        new ValueObject\Type(StringBackedDummy::class, false),
                        null,
                    ),
                ],
                new ValueObject\Type(StringBackedDummy::class),
            ),
            new ValueObject\DaggerFunction(
                'defaultStringBackedEnum',
                'Test handling of defaults for custom string-backed enums',
                [
                    new ValueObject\Argument(
                        'arg',
                        '',
                        new ValueObject\Type(StringBackedDummy::class, false),
                        new Json('"hello, "'),
                    ),
                ],
                new ValueObject\Type(StringBackedDummy::class),
            ),
        ]);
    }
}
