<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, Doc};
use Dagger\Directory;
use function Dagger\dag;

#[DaggerObject]
#[Doc('A module for editing code')]
class Workspace
{
  #[Doc('The source directory')]
  public Directory $source;

  #[DaggerFunction]
  public function __construct(Directory $source)
  {
    $this->source = $source;
  }

  #[DaggerFunction]
  #[Doc('Read a file in the Workspace')]
  public function readFile(#[Doc('The path to the file in the workspace')] string $path): string
  {
    return $this->source->file($path)->contents();
  }

  #[DaggerFunction]
  #[Doc('Write a file to the Workspace')]
  public function writeFile(
    #[Doc('The path to the file in the workspace')] string $path,
    #[Doc('The new contents of the file')] string $contents
  ): Workspace {
    $this->source = $this->source->withNewFile($path, $contents);
    return $this;
  }

  #[DaggerFunction]
  #[Doc('List all of the files in the Workspace')]
  public function listFiles(): string
  {
    return dag()
      ->container()
      ->from('alpine:3')
      ->withDirectory('/src', $this->source)
      ->withWorkdir('/src')
      ->withExec(['tree', './src'])
      ->stdout();
  }

  #[DaggerFunction]
  #[Doc('Get the source code directory from the Workspace')]
  public function getSource(): Directory
  {
    return $this->source;
  }

  #[DaggerFunction]
  #[Doc('Return the result of running unit tests')]
  public function test(): string
  {
    $nodeCache = dag()->cacheVolume('node');
    return dag()
      ->container()
      ->from('node:21-slim')
      ->withDirectory('/src', $this->source)
      ->withMountedCache('/root/.npm', $nodeCache)
      ->withWorkdir('/src')
      ->withExec(['npm', 'install'])
      ->withExec(['npm', 'run', 'test:unit', 'run'])
      ->stdout();
  }
}
