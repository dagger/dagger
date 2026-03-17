<?php

declare(strict_types=1);

namespace Dagger\Exception\RegistrationError;

use Dagger\Exception\RegistrationError;

final class MissingAttribute extends \RuntimeException implements RegistrationError
{
    public static function listOfType(
        string $methodName,
        string $parameterName,
    ): self {
        $missingAttribute = \Dagger\Attribute\ListOfType::class;
        return new self(
            "DaggerFunction '$methodName' takes array argument '$parameterName'"
            . ", array arguments require $missingAttribute",
        );
    }

    public static function returnsListOfType(
        string $methodName,
    ): self {
        $missingAttribute = \Dagger\Attribute\ReturnsListOfType::class;
        return new self(
            "DaggerFunction '$methodName' returns an array"
            . ", Dagger Functions returning arrays require $missingAttribute",
        );
    }
}
