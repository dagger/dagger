<?php

namespace Dagger\Exception;

use RuntimeException;
use Throwable;

final class CliFallbackFailed extends RuntimeException
{
    public function __construct(
        private readonly CliReleaseUnavailable $downloadError,
        private readonly Throwable $fallbackError,
        string $context,
    ) {
        parent::__construct(
            sprintf(
                "%s\n%s: %s",
                $downloadError->getMessage(),
                $context,
                $fallbackError->getMessage(),
            ),
            previous: $fallbackError,
        );
    }

    public function getDownloadError(): CliReleaseUnavailable
    {
        return $this->downloadError;
    }

    public function getFallbackError(): Throwable
    {
        return $this->fallbackError;
    }
}
