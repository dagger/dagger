<?php

/**
 * This class has been generated by dagger-php-sdk. DO NOT EDIT.
 */

declare(strict_types=1);

namespace DaggerIo\Gen;

/**
 * Compression algorithm to use for image layers.
 */
enum ImageLayerCompression: string
{
    case EStarGZ = 'EStarGZ';
    case Gzip = 'Gzip';
    case Uncompressed = 'Uncompressed';
    case Zstd = 'Zstd';
}
