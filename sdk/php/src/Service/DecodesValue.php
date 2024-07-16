<?php

declare(strict_types=1);

namespace Dagger\Service;

use Dagger\Client;
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

    private function decodeType(string $value, Type $type): mixed
    {
        switch ($type->typeDefKind) {
            case TypeDefKind::BOOLEAN_KIND:
            case TypeDefKind::INTEGER_KIND:
            case TypeDefKind::STRING_KIND:
                return json_decode($value, true);
            case TypeDefKind::SCALAR_KIND:
                return new ($type->name)($value);
            case TypeDefKind::VOID_KIND:
                return null;
            case TypeDefKind::ENUM_KIND:
                return ($type->name)::from($value);
            case TypeDefKind::INTERFACE_KIND:
                throw new RuntimeException(sprintf(
                    'Currently cannot decode custom interfaces: %s',
                    $type->name
                ));
            case TypeDefKind::OBJECT_KIND:
                if ($type->isIdable()) {
                    $method = sprintf('load%sFromId', $type->getShortName());
                    $id = sprintf('%sId', $type->name);

                    return $this->client->$method(new $id(json_decode($value)));
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
