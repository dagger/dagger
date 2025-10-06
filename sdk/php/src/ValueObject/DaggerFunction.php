<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;
use Dagger\Exception\RegistrationError\MissingAttribute;
use Dagger\Exception\UnsupportedType;
use ReflectionMethod;
use ReflectionNamedType;
use RuntimeException;

/** @internal Value Object used for methods to expose to Dagger. */
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
        if ($method->getAttributes(Attribute\DaggerFunction::class) === []) {
            throw new RuntimeException(sprintf(
                'Method "%s" is not considered a dagger function without the %s attribute',
                $method->getName(),
                Attribute\DaggerFunction::class
            ));
        }

        $description = (current($method
            ->getAttributes(Attribute\Doc::class)) ?: null)
            ?->newInstance()
            ?->description;

        $parameters = array_map(
            fn($p) => Argument::fromReflection($p),
            $method->getParameters(),
        );

        return $method->isConstructor() ?
            new self(
                name: '',
                description: null,
                arguments: $parameters,
                returnType: new Type($method->getDeclaringClass()->name)
            ) :
            new self(
                name: $method->name,
                description: $description,
                arguments: $parameters,
                returnType: self::getReturnType($method),
            );
    }

    private static function getReturnType(
        ReflectionMethod $method
    ): ListOfType|Type {
        $type = $method->getReturnType() ?? throw new RuntimeException(sprintf(
            'DaggerFunction "%s" cannot be supported without a return type',
            $method->name,
        ));

        if (!($type instanceof ReflectionNamedType)) {
            throw new UnsupportedType(
                'The PHP SDK only supports named types and nullable named types',
            );
        }

        if ($type->getName() === 'array') {
            $attribute = (current($method
                ->getAttributes(Attribute\ReturnsListOfType::class)) ?: null)
                ?->newInstance()
            ?? throw MissingAttribute::returnsListOfType($method->getName());

            return ListOfType::fromReflection($type, $attribute);
        }

        return Type::fromReflection($type);
    }
}
