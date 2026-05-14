<?php

declare(strict_types=1);

namespace Dagger\Service;

use Dagger\Exception\UnsupportedType;
use Dagger\TypeDefKind;
use Dagger\ValueObject;
use ReflectionEnum;

final class FindsDaggerEnums
{
    /**
     * Finds all backed enums that should be registered as Dagger enum types.
     *
     * Enums are auto-discovered by walking the type-graph of already-known
     * DaggerObjects: any backed enum that appears as a method parameter type,
     * return type, or field type is included. No explicit annotation is needed
     * on the enum itself — this matches Go and TypeScript SDK behaviour.
     *
     * Results are deduplicated by FQN. Enums must be registered before the
     * objects that reference them (callers should register the result of this
     * service before iterating DaggerObjects).
     *
     * @param ValueObject\DaggerObject[] $daggerObjects
     * @return ValueObject\DaggerEnum[]
     */
    public function __invoke(array $daggerObjects): array
    {
        $fqns = [];

        foreach ($daggerObjects as $daggerObject) {
            foreach ($daggerObject->daggerFunctions as $daggerFunction) {
                foreach ($daggerFunction->arguments as $argument) {
                    $this->collectEnumFqn($argument->type, $fqns);
                }
                $this->collectEnumFqn($daggerFunction->returnType, $fqns);
            }
            foreach ($daggerObject->daggerFields as $daggerField) {
                $this->collectEnumFqn($daggerField->type, $fqns);
            }
        }

        $result = [];
        foreach (array_keys($fqns) as $fqn) {
            if (!enum_exists($fqn)) {
                continue;
            }
            $reflEnum = new ReflectionEnum($fqn);
            if (!$reflEnum->isBacked()) {
                throw new UnsupportedType(sprintf(
                    'Enum %s must be a backed enum (string or int) to be used as a Dagger type.',
                    $fqn,
                ));
            }
            $result[] = ValueObject\DaggerEnum::fromReflection($reflEnum);
        }

        return $result;
    }

    /**
     * @param array<string, true> $fqns
     */
    private function collectEnumFqn(
        ValueObject\ListOfType|ValueObject\Type $type,
        array &$fqns,
    ): void {
        if ($type instanceof ValueObject\ListOfType) {
            $this->collectEnumFqn($type->subtype, $fqns);
            return;
        }

        if ($type->typeDefKind === TypeDefKind::ENUM_KIND) {
            $fqns[$type->name] = true;
        }
    }
}
