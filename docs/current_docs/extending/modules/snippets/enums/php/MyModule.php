<?php

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject};
use DaggerModule\Severity;

use function Dagger\dag;

#[DaggerObject] class MyModule
{
    #[DaggerFunction] public function scan(
        string $ref,
        Severity $severity,
    ): string {
        $ctr = dag()->container()->from($ref);

        return dag()
            ->container()
            ->from('aquasec/trivy:0.50.4')
            ->withMountedFile('/mnt/ctr.tar', ctr->asTarball())
            ->withMountedCache('/root/.cache', dag()->cacheVolume('trivy-cache'))
            ->withExec([
                'trivy',
                'image',
                '--format=json',
                '--no-progress',
                '--exit-code=1',
                '--vuln-type=os,library',
                "--severity=$severity->value",
                '--show-suppressed',
                '--input=/mnt/ctr.tar',
            ])
            ->stdout();
    }
}
