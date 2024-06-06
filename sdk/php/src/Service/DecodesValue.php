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
     */
    public function __invoke(string $value, string $type): mixed
    {
        try {
            return $this->convertValue($type, json_decode($value, true));
        } catch (UnhandledMatchError $e) {
            throw new RuntimeException(sprintf('cannot decode:%s', $type));
        }

    }

    private function convertValue(string $type, mixed $value): mixed
    {
        // todo mirror the getTypeDefFromPHPType switch statement
        // todo work out what happens if null
        return match ($type) {
            'string', 'int', 'bool' => $value,
            Directory::class => $this->client
                ->loadDirectoryFromID(new DirectoryId($value)),
            Container::class => $this->client
                ->loadContainerFromID(new ContainerId($value)),
            File::class => $this->client
                ->loadFileFromID(new FileId($value))
        };
    }
}
