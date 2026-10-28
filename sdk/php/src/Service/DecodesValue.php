<?php

declare(strict_types=1);

namespace Dagger\Service;

use Dagger\Client;
use Dagger\Id;
use Dagger\TypeDefKind;
use Dagger\ValueObject\ListOfType;
use Dagger\ValueObject\Type;
use RuntimeException;

final readonly class DecodesValue
{
    public function __construct(
        private Client $client,
    ) {
    }

    /**
     * Converts a json_encoded value to the given type.
     * @throws RuntimeException
     * if no support exists for decoding the type given
     */
    public function __invoke(string $value, ListOfType|Type $type): mixed
    {
        if ($type->nullable && in_array($value, ['', 'null'])) {
            return null;
        }

        return $type instanceof Type ?
            $this->decodeType($value, $type) :
            $this->decodeListOfType($value, $type);
    }

    private function decodeListOfType(string $value, ListOfType $list): mixed
    {
        if (preg_match('#^\[.*]$#', $value) !== 1) {
            throw new RuntimeException(sprintf(
                '"%s" has unbalanced square brackets',
                $value,
            ));
        }

        $valueWithoutOuterBrackets = substr($value, 1, strlen($value) - 2);

        return array_map(
            fn($v) => $this($v, $list->subtype),
            explode(',', $valueWithoutOuterBrackets),
        );
    }

    /**
     * The engine identifies an enum member by its case name, not by the
     * enum's backing value, so resolve the case rather than calling from().
     *
     * @param class-string $enum
     * @throws RuntimeException if the name is not a case of the enum
     */
    private static function decodeEnumCase(string $enum, mixed $case): \UnitEnum
    {
        $reflection = new \ReflectionEnum($enum);

        if (is_string($case)) {
            try {
                return $reflection->getCase($case)->getValue();
            } catch (\ReflectionException) {
                // Fall through to the error below, which lists the cases.
            }
        }

        throw new RuntimeException(sprintf(
            "'%s' is not a case of '%s', available cases are: '%s'",
            is_string($case) ? $case : get_debug_type($case),
            $enum,
            implode("', '", array_map(
                fn($c) => $c->getName(),
                $reflection->getCases(),
            )),
        ));
    }

    private function decodeType(string $value, Type $type): mixed
    {
        switch ($type->typeDefKind) {
            case TypeDefKind::BOOLEAN_KIND:
            case TypeDefKind::INTEGER_KIND:
            case TypeDefKind::FLOAT_KIND:
            case TypeDefKind::STRING_KIND:
                return json_decode($value, true);
            case TypeDefKind::SCALAR_KIND:
                return new ($type->name)($value);
            case TypeDefKind::VOID_KIND:
                return null;
            case TypeDefKind::ENUM_KIND:
                return self::decodeEnumCase($type->name, json_decode($value));
            case TypeDefKind::INTERFACE_KIND:
                throw new RuntimeException(sprintf(
                    'Currently cannot decode custom interfaces: %s',
                    $type->name
                ));
            case TypeDefKind::OBJECT_KIND:
                if ($type->isIdable()) {
                    return $this->client->loadObjectFromId(
                        $type->name,
                        new Id(json_decode($value)),
                    );
                }

                throw new RuntimeException(sprintf(
                    'Currently cannot decode custom classes: %s',
                    $type->name
                ));
            default:
                throw new RuntimeException("Cannot decode $type->name");
        }
    }
}
