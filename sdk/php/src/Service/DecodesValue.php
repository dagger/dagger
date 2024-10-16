<?php

declare(strict_types=1);

namespace Dagger\Service;

use Dagger\Client;
use Dagger\TypeDefKind;
use Dagger\ValueObject\TypeHint;
use Dagger\ValueObject\TypeHint\ListOfType;
use Dagger\ValueObject\TypeHint\Type;
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
    public function __invoke(string $value, TypeHint $type): mixed
    {
        if ($type->isNullable() && in_array($value, ['', 'null'])) {
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
            fn($v) => $this($v, $list->getSubtype()),
            explode(',', $valueWithoutOuterBrackets),
        );
    }

    private function decodeType(string $value, Type $type): mixed
    {
        switch ($type->getTypeDefKind()) {
            case TypeDefKind::BOOLEAN_KIND:
            case TypeDefKind::INTEGER_KIND:
            case TypeDefKind::STRING_KIND:
                return json_decode($value, true);
            case TypeDefKind::SCALAR_KIND:
                return new ($type->getName())($value);
            case TypeDefKind::VOID_KIND:
                return null;
            case TypeDefKind::ENUM_KIND:
                return ($type->getName())::from($value);
            case TypeDefKind::INTERFACE_KIND:
                throw new RuntimeException(sprintf(
                    'Currently cannot decode custom interfaces: %s',
                    $type->getName(),
                ));
            case TypeDefKind::OBJECT_KIND:
                if ($type->isIdable()) {
                    $method = sprintf('load%sFromId', NormalizesClassName::shorten($type->getName()));
                    $id = sprintf('%sId', $type->getName());

                    return $this->client->$method(new $id(json_decode($value)));
                }

                throw new RuntimeException(sprintf(
                    'Currently cannot decode custom classes: %s',
                    $type->getName(),
                ));
            default:
                throw new RuntimeException("Cannot decode {$type->getName()}");
        }
    }
}
