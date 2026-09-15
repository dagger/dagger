<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;

final readonly class DaggerObject
{
    /**
     * @var array<string, DaggerField>
     *            name => DaggerField
     */
    public array $daggerFields;

    /**
     * @var array<string, DaggerFunction>
     *            name => DaggerFunction
     */
    public array $daggerFunctions;

    /**
     * @param DaggerField[] $fields
     * @param DaggerFunction[] $functions
     */
    public function __construct(
        public string $name,
        public string $description = '',
        array $fields = [],
        array $functions = [],
    ) {
        $this->ensureUniqueNames($fields, $functions);

        $this->daggerFields = array_combine(
            array_map(fn($f) => $f->name, $fields),
            $fields,
        );

        $this->daggerFunctions = array_combine(
            array_map(fn($f) => $f->name, $functions),
            $functions,
        );
    }

    /**
     * @throws \RuntimeException
     * - if missing DaggerObject Attribute
     * - if any DaggerFunction parameter type is unsupported
     * - if any DaggerFunction return type is unsupported
     */
    public static function fromReflection(\ReflectionClass $class): self
    {
        if (empty($class->getAttributes(Attribute\DaggerObject::class))) {
            throw new \RuntimeException('class is not a DaggerObject');
        }

        $description = (current($class
            ->getAttributes(Attribute\Doc::class)) ?: null)
            ?->newInstance()
            ->description
            ?? '';

        $fieldReflections = array_filter(
            $class->getProperties(\ReflectionProperty::IS_PUBLIC),
            fn($f) => !empty($f->getAttributes(Attribute\DaggerFunction::class)),
        );

        $daggerFields = array_map(
            fn($r) => DaggerField::fromReflection($r),
            $fieldReflections,
        );

        $methodReflections = array_filter(
            $class->getMethods(\ReflectionMethod::IS_PUBLIC),
            fn($m) => !empty($m->getAttributes(Attribute\DaggerFunction::class)),
        );

        $daggerFunctions = array_map(
            fn($r) => DaggerFunction::fromReflection($r),
            $methodReflections,
        );

        return new self(
            name: $class->name,
            description: $description,
            fields: $daggerFields,
            functions: $daggerFunctions
        );
    }

    public function requiresConstruction(): bool
    {
        return ! empty($this->daggerFields);
    }

    public function hasConstructor(): bool
    {
        return ! empty(array_filter(
            $this->daggerFunctions,
            fn($f) => $f->isConstructor(),
        ));
    }

    /**
     * @param DaggerField[] $fields
     * @param DaggerFunction[] $functions
     */
    private static function ensureUniqueNames(
        array $fields,
        array $functions,
    ): void {
        $fields = array_combine(
            array_map(fn($f) => $f->name, $fields),
            array_map(fn($f) => ucfirst($f->name), $fields),
        );
        $functions = array_combine(
            array_map(fn($f) => $f->name, $functions),
            array_map(fn($f) => ucfirst($f->name), $functions),
        );

        while ($fields !== []) {
            $name = array_key_last($fields);
            $nameNormalized = array_pop($fields);

            foreach ($fields as $other => $otherNormalized) {
                if ($otherNormalized === $nameNormalized) {
                    throw new \RuntimeException(
                        "Fields; '$name' and '$other' conflict",
                    );
                }
            }

            foreach ($functions as $other => $otherNormalized) {
                if ($otherNormalized === $nameNormalized) {
                    throw new \RuntimeException(
                        "Field; '$name' conflicts with function; '$other'",
                    );
                }
            }
        }

        while ($functions !== []) {
            $name = array_key_last($functions);
            $nameNormalized = array_pop($functions);

            foreach ($functions as $other => $otherNormalized) {
                if ($otherNormalized === $nameNormalized) {
                    throw new \RuntimeException(
                        "Functions; '$name' and '$other' conflict",
                    );
                }
            }
        }
    }
}
