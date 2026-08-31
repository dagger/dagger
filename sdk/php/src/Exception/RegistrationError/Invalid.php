<?php

declare(strict_types=1);

namespace Dagger\Exception\RegistrationError;

use Dagger\Exception\RegistrationError;

final class Invalid extends \RuntimeException implements RegistrationError
{
    public static function checkTakesNoArgs(
        string $methodName,
        string $argName,
    ): self {
        return new self(<<<TEXT
            '$methodName' is labeled as a check, but requires '$argName'.
            Checks may only contain optional arguments.
            TEXT);
    }

    public static function checkReturnsContainerOrVoid(
        string $methodName,
        string $returnType,
    ): self {
        return new self(<<<TEXT
            '$methodName' is labeled as a check, but returns '$returnType'.
            Checks must return Containers or void.
            TEXT);
    }
}
