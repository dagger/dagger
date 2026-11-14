<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit;

use Dagger\Artifact;
use Dagger\Artifacts;
use Dagger\Client\AbstractClient;
use Dagger\GraphQl\QueryBuilderChain;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
class ArtifactDimensionQueryTest extends TestCase
{
    public function testDefaultQueriesKeepTheOldShapeAndTrueAddsTheArgument(): void
    {
        $client = $this->getMockBuilder(AbstractClient::class)
            ->disableOriginalConstructor()
            ->onlyMethods(['queryLeaf'])
            ->getMock();
        $queries = [];
        $client->expects(self::exactly(12))->method('queryLeaf')->willReturnCallback(
            static function ($query) use (&$queries): array {
                $queries[] = (string) $query;
                return [];
            }
        );
        foreach ([[Artifact::class, 'dimensionKeys'], [Artifacts::class, 'dimensions']] as [$class, $method]) {
            $object = new $class($client, new QueryBuilderChain());
            $object->$method();
            $object->$method(false);
            $object->$method(null);
            $object->$method(true);
            self::assertSame([
                'query { ' . $method . ' }',
                'query { ' . $method . ' }',
                'query { ' . $method . ' }',
                'query { ' . $method . '(all: true) }',
            ], $queries);
            $queries = [];
        }
        $artifacts = new Artifacts($client, new QueryBuilderChain());
        $artifacts->filterDimensions(['Tests'])->dimensions();
        $artifacts->filterDimensions(['Tests'], false)->dimensions();
        $artifacts->filterDimensions(['Tests'], null)->dimensions();
        $artifacts->filterDimensions(['Tests'], true)->dimensions();
        self::assertSame([
            'query { filterDimensions(dimensions: ["Tests"]) { dimensions } }',
            'query { filterDimensions(dimensions: ["Tests"]) { dimensions } }',
            'query { filterDimensions(dimensions: ["Tests"]) { dimensions } }',
            'query { filterDimensions(dimensions: ["Tests"] exclude: true) { dimensions } }',
        ], $queries);
    }
}
