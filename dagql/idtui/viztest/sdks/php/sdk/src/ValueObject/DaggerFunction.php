<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Agent;
use Dagger\Attribute;
use Dagger\Changeset;
use Dagger\Container;
use Dagger\Exception\RegistrationError\Invalid;
use Dagger\Exception\RegistrationError\MissingAttribute;
use Dagger\Exception\UnsupportedType;
use Dagger\LLM;
use Dagger\Service;
use Dagger\Workspace;
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
        public bool $withCheck = false,
        public bool $withGenerator = false,
        public bool $withUp = false,
        public bool $withAgent = false,
    ) {
        if ($this->withCheck) {
            $this->validateRole('a check', 'Checks', [Container::class, 'void'], 'Containers or void', mayBeNull: true);
        }
        if ($this->withGenerator) {
            $this->validateRole('a generator', 'Generators', [Changeset::class], 'a non-null Changeset');
        }
        if ($this->withUp) {
            $this->validateRole('up', 'Up functions', [Service::class], 'a non-null Service');
        }
        if ($this->withAgent) {
            $this->validateAgent();
        }
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

        $constructor = $method->isConstructor();

        return new self(
            name: $constructor ? '' : $method->name,
            description: $constructor ? null : $description,
            arguments: $parameters,
            returnType: $constructor ? new Type($method->getDeclaringClass()->name) : self::getReturnType($method),
            withCheck: $method->getAttributes(Attribute\Check::class) !== [],
            withGenerator: $method->getAttributes(Attribute\Generate::class) !== [],
            withUp: $method->getAttributes(Attribute\Up::class) !== [],
            withAgent: $method->getAttributes(Attribute\Agent::class) !== [],
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

    /** @param list<string> $returns */
    private function validateRole(
        string $role,
        string $plural,
        array $returns,
        string $expected,
        bool $mayBeNull = false,
    ): void {
        foreach ($this->arguments as $arg) {
            if ($arg->isRequired() && !($arg->type instanceof Type && Workspace::class === $arg->type->name)) {
                throw Invalid::requires($this->name, $role, $arg->name, "$plural may only require a Workspace");
            }
        }

        $this->validateReturn($role, $plural, $returns, $expected, $mayBeNull);
    }

    /** @param list<string> $returns */
    private function validateReturn(
        string $role,
        string $plural,
        array $returns,
        string $expected,
        bool $mayBeNull = false,
    ): void {
        $returnType = $this->returnType instanceof Type ? $this->returnType->name : 'array';
        if (!in_array($returnType, $returns, true) || ($this->returnType->nullable && !$mayBeNull)) {
            throw Invalid::returns($this->name, $role, $returnType, "$plural must return $expected");
        }
    }

    private function validateAgent(): void
    {
        $this->validateReturn('an agent', 'Agents', [LLM::class], 'a non-null LLM');

        $llms = 0;
        $bases = 0;
        foreach ($this->arguments as $arg) {
            $type = $arg->type instanceof Type ? $arg->type->name : 'array';
            $llms += LLM::class === $type ? 1 : 0;
            if (!$arg->isRequired() || Workspace::class === $type || Agent::class === $type) {
                continue;
            }
            if (LLM::class === $type && ++$bases === 1) {
                continue;
            }
            throw Invalid::requires(
                $this->name,
                'an agent',
                $arg->name,
                'Agents may only require their base LLM, a Workspace or an Agent',
            );
        }

        if ($bases !== 1 || $llms !== 1) {
            throw Invalid::agentTakesOneBase($this->name);
        }
    }
}
