<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;
use Dagger\Client\IdAble;
use Dagger\Json;
use ReflectionParameter;
use Roave\BetterReflection\Reflection\ReflectionParameter as BetterReflectionParameter;

final readonly class DaggerArgument
{
    public function __construct(
        public string $name,
        public ?string $description,
        public Type $type,
        public ?Json $default = null,
    ) {

    }

    public static function fromReflection(ReflectionParameter $parameter): self
    {
        $attribute = (current($parameter
            ->getAttributes(Attribute\DaggerArgument::class)) ?: null)
            ?->newInstance();

        return new self(
            $parameter->name,
            $attribute?->description,
            Type::fromReflection($parameter->getType()),
            self::getDefault($parameter),
        );
    }

    private static function getDefault(ReflectionParameter $parameter): ?Json
    {
        if ($parameter->isDefaultValueAvailable()) {
            $betterReflection = BetterReflectionParameter
                ::createFromClassNameAndMethod(
                    $parameter->getDeclaringClass()->getName(),
                    $parameter->getDeclaringFunction()->getName(),
                    $parameter->getName(),
                );
            $default = $betterReflection->getDefaultValue();
            return new Json(json_encode(
                $default instanceof IdAble ? (string) $default->id() : $default
            ));
        }

        if ($parameter->allowsNull()) {
            return new Json(json_encode(null));
        }

        return null;
    }
}
