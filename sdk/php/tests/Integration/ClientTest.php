<?php

namespace Dagger\Tests\Integration;

use Dagger\Client;
use Dagger\Dagger;
use Dagger\PipelineLabel;
use GraphQL\QueryBuilder\QueryBuilder;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\TestCase;

#[Group('integration')]
class ClientTest extends TestCase
{
    public function newClient(): Client
    {
        return Dagger::connect();
    }

    public function testQueryBuilder(): void
    {
        $client = $this->newClient();
        $qb = new QueryBuilder();
        $qb->selectField(
            (new QueryBuilder('directory'))->selectField(
                (new QueryBuilder('withNewFile'))
                    ->setArgument('path', '/hello.txt')
                    ->setArgument('contents', 'world')
                    ->selectField(
                        (new QueryBuilder('file'))
                            ->setArgument('path', '/hello.txt')
                            ->selectField('contents')
                    )
            )
        );

        $result = $client->queryLeaf($qb, 'contents');
        $this->assertEquals('world', $result);
    }

    public function testDirectory(): void
    {
        $client = $this->newClient();
        $dir = $client->directory();
        $content = $dir
                ->withNewFile('/hello.txt', 'world')
                ->file('/hello.txt')
                ->contents();

        $this->assertEquals('world', $content);
    }

    public function testContainer(): void
    {
        $client = $this->newClient();
        $alpine = $client->container()->from('alpine:3.16.2');

        $content = $alpine->rootfs()->file('/etc/alpine-release')->contents();
        $this->assertEquals('3.16.2', trim($content));

        $stdout = $alpine->withExec(['cat', '/etc/alpine-release'])->stdout();
        $this->assertEquals('3.16.2', trim($stdout));

        $contents = $client->loadContainerFromID($alpine->id())
            ->rootfs()
            ->file('/etc/alpine-release')
            ->contents();

        $this->assertEquals('3.16.2', trim($contents));
    }

    public function testPipeline()
    {
        $client = $this->newClient();
        $stdout = $client->pipeline('test', 'pipeline description', [
            new PipelineLabel('distribution', 'alpine'),
        ])
            ->container()
            ->from('alpine:3.16.2')
            ->withExec(['cat', '/etc/alpine-release'])
            ->stdout();

        $this->assertEquals('3.16.2', trim($stdout));
    }
}
