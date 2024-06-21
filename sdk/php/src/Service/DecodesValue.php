<?php

declare(strict_types=1);

namespace Dagger\Service;

use Dagger\Client;
use Dagger\Container;
use Dagger\ContainerId;
use Dagger\Directory;
use Dagger\DirectoryId;
use Dagger\File;
use Dagger\FileId;
use Dagger\TypeDefKind;
use Dagger\ValueObject\Type;
use RuntimeException;
use UnhandledMatchError;

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
    public function __invoke(string $value, Type $type): mixed
    {
        switch ($type->typeDefKind) {
            case TypeDefKind::BOOLEAN_KIND:
            case TypeDefKind::INTEGER_KIND:
            case TypeDefKind::STRING_KIND:
                return json_decode($value, true);
            case TypeDefKind::VOID_KIND:
                return null;
            case TypeDefKind::LIST_KIND:
                throw new RuntimeException('Currently cannot decode arrays');
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
