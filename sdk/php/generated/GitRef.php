<?php

/**
 * This class has been generated by dagger-php-sdk. DO NOT EDIT.
 */

declare(strict_types=1);

namespace Dagger;

/**
 * A git ref (tag, branch, or commit).
 */
class GitRef extends Client\AbstractObject implements Client\IdAble
{
    /**
     * The resolved commit id at this ref.
     */
    public function commit(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('commit');
        return (string)$this->queryLeaf($leafQueryBuilder, 'commit');
    }

    /**
     * A unique identifier for this GitRef.
     */
    public function id(): GitRefId
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('id');
        return new \Dagger\GitRefId((string)$this->queryLeaf($leafQueryBuilder, 'id'));
    }

    /**
     * The resolved ref name at this ref.
     */
    public function ref(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('ref');
        return (string)$this->queryLeaf($leafQueryBuilder, 'ref');
    }

    /**
     * The filesystem tree at this ref.
     */
    public function tree(?bool $discardGitDir = false, ?int $depth = 1): Directory
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('tree');
        if (null !== $discardGitDir) {
        $innerQueryBuilder->setArgument('discardGitDir', $discardGitDir);
        }
        if (null !== $depth) {
        $innerQueryBuilder->setArgument('depth', $depth);
        }
        return new \Dagger\Directory($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }
}
