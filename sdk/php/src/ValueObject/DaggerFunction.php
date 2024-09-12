<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;
use ReflectionMethod;
use RuntimeException;

final readonly class DaggerFunction
{
    /** @param Argument[] $arguments */
    public function __construct(
        public string $name,
        public ?string $description,
        public array $arguments,
        public ListOfType|Type $returnType,
    ) {
    }

    public function isConstructor(): bool
    {
        return $this->name === '';
    }

    /**
     * @throws RuntimeException
     * - if missing DaggerFunction Attribute
     * - if any parameter types are unsupported
     * - if the return type is unsupported
     */
    public static function fromReflection(ReflectionMethod $method): self
    {
        $attribute = (current($method
            ->getAttributes(Attribute\DaggerFunction::class)) ?: null)
            ?->newInstance() ??
            throw new RuntimeException('method is not a DaggerFunction');

        $parameters = array_map(
            fn($p) => Argument::fromReflection($p),
            $method->getParameters(),
        );

        return $method->isConstructor() ?
            new self(
                '',
                null,
                $parameters,
                new Type($method->getDeclaringClass()->name)
            ) :
            new self(
                $method->name,
                $attribute->description,
                $parameters,
                self::getReturnType($method),
            );
    }

    private static function getReturnType(
        ReflectionMethod $method
    ): null|ListOfType|Type {
        $type = $method->getReturnType() ?? throw new RuntimeException(sprintf(
            'DaggerFunction "%s" cannot be supported without a return type',
            $method->name,
        ));

        $attribute = (current($method
            ->getAttributes(Attribute\ReturnsListOfType::class)) ?: null)
            ?->newInstance();

        return isset($attribute) ?
            ListOfType::fromReflection($type, $attribute) :
            Type::fromReflection($type);
    }
}
