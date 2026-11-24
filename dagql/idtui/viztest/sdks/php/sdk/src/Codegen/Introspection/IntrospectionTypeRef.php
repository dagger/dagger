<?php

declare(strict_types=1);

namespace Dagger\Codegen\Introspection;

class IntrospectionTypeRef
{
    public string $kind;
    public ?string $name;
    public ?IntrospectionTypeRef $ofType;

    public static function fromArray(?array $data): ?self
    {
        if ($data === null) {
            return null;
        }

        $ref = new self();
        $ref->kind = $data['kind'];
        $ref->name = $data['name'] ?? null;
        $ref->ofType = isset($data['ofType']) ? self::fromArray($data['ofType']) : null;

        return $ref;
    }

    public function isNonNull(): bool
    {
        return $this->kind === 'NON_NULL';
    }

    public function isList(): bool
    {
        $ref = $this;
        if ($ref->kind === 'NON_NULL') {
            $ref = $ref->ofType;
        }
        return $ref->kind === 'LIST';
    }

    public function isScalar(): bool
    {
        $ref = $this;
        if ($ref->kind === 'NON_NULL') {
            $ref = $ref->ofType;
        }
        return $ref->kind === 'SCALAR';
    }

    public function isObject(): bool
    {
        $ref = $this;
        if ($ref->kind === 'NON_NULL') {
            $ref = $ref->ofType;
        }
        return $ref->kind === 'OBJECT';
    }

    public function isInterface(): bool
    {
        $ref = $this;
        if ($ref->kind === 'NON_NULL') {
            $ref = $ref->ofType;
        }
        return $ref->kind === 'INTERFACE';
    }

    public function isEnum(): bool
    {
        $ref = $this;
        if ($ref->kind === 'NON_NULL') {
            $ref = $ref->ofType;
        }
        return $ref->kind === 'ENUM';
    }

    public function isInputObject(): bool
    {
        $ref = $this;
        if ($ref->kind === 'NON_NULL') {
            $ref = $ref->ofType;
        }
        return $ref->kind === 'INPUT_OBJECT';
    }

    /**
     * Get the leaf type name, unwrapping NON_NULL and LIST wrappers.
     */
    public function leafName(): ?string
    {
        if ($this->name !== null) {
            return $this->name;
        }
        if ($this->ofType !== null) {
            return $this->ofType->leafName();
        }
        return null;
    }

    /**
     * Returns true if this is the built-in ID scalar type.
     */
    public function isIDScalar(): bool
    {
        $ref = $this;
        if ($ref->kind === 'NON_NULL') {
            $ref = $ref->ofType;
        }
        return $ref->kind === 'SCALAR' && $ref->name === 'ID';
    }

    /**
     * Check if this is a built-in GraphQL scalar (String, Int, Float, Boolean).
     */
    public function isBuiltinScalar(): bool
    {
        $name = $this->leafName();
        return in_array($name, ['String', 'Int', 'Float', 'Boolean'], true);
    }

    /**
     * Check if this is the Void custom scalar.
     */
    public function isVoid(): bool
    {
        return $this->leafName() === 'Void';
    }

    /**
     * Check if this is the DateTime custom scalar.
     */
    public function isDateTime(): bool
    {
        return $this->leafName() === 'DateTime';
    }

    /**
     * Check if this is a custom scalar (not built-in, not ID).
     */
    public function isCustomScalar(): bool
    {
        return $this->isScalar() && !$this->isBuiltinScalar() && !$this->isIDScalar();
    }
}
