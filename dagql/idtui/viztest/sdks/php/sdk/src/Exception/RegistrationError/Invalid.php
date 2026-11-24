<?php

declare(strict_types=1);

namespace Dagger\Exception\RegistrationError;

use Dagger\Exception\RegistrationError;

final class Invalid extends \RuntimeException implements RegistrationError
{
    public static function requires(string $methodName, string $role, string $argName, string $rule): self
    {
        return new self(<<<TEXT
            '$methodName' is labeled as $role, but requires '$argName'.
            $rule; every other argument must be optional.
            TEXT);
    }

    public static function returns(string $methodName, string $role, string $returnType, string $rule): self
    {
        return new self(<<<TEXT
            '$methodName' is labeled as $role, but returns '$returnType'.
            $rule.
            TEXT);
    }

    public static function agentTakesOneBase(string $methodName): self
    {
        return new self(<<<TEXT
            '$methodName' is labeled as an agent, but does not require exactly one LLM.
            Agents must require one non-null LLM, the base they extend, and take no other LLM.
            TEXT);
    }
}
