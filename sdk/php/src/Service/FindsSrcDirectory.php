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

final readonly class FindsSrcDirectory
{
    /**
     * Find the Module "src" directory
     *
     * @param null|string $dir
     * The directory to start searching from.
     * If unspecified the current working directory is used
     */
    public function __invoke(?string $dir = null): string
    {
        $dir = is_null($dir) ? __DIR__ : rtrim($dir, '/');

        return $this->searchDirectory($dir) ??
            $this->searchUpwards($dir) ??
            throw new RuntimeException('Cannot find module src directory');
    }

    private function searchDirectory(string $dir): ?string
    {
        return file_exists("$dir/dagger") && is_dir("$dir/src") ?
            "$dir/src" :
            null;
    }

    private function searchUpwards(string $dir): ?string
    {
        $parentDir = dirname($dir);

        return $parentDir === '.' ?
            null :
            $this->searchDirectory($parentDir) ??
            $this->searchUpwards($parentDir);
    }
}
