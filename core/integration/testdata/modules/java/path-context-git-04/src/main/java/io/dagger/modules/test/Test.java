package io.dagger.modules.test;


import io.dagger.client.GitRef;
import io.dagger.client.GitRepository;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.DefaultPath;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.concurrent.ExecutionException;

@Object
public class Test {
    @Function
    public String testRepoLocal(@DefaultPath("./.git") GitRepository git) throws ExecutionException, DaggerQueryException, InterruptedException {
        return this.commitAndRef(git.head());
    }

    @Function
    public String testRepoLocalAbs(@DefaultPath("/") GitRepository git) throws ExecutionException, DaggerQueryException, InterruptedException {
        return this.commitAndRef(git.head());
    }

    @Function
    public String testRepoRemote(@DefaultPath("https://github.com/dagger/dagger.git") GitRepository git) throws ExecutionException, DaggerQueryException, InterruptedException {
        return this.commitAndRef(git.tag("v0.18.2"));
    }

    @Function
    public String testRefLocal(@DefaultPath("./.git") GitRef git) throws ExecutionException, DaggerQueryException, InterruptedException {
        return this.commitAndRef(git);
    }

    @Function
    public String testRefRemote(@DefaultPath("https://github.com/dagger/dagger.git#v0.18.3") GitRef git) throws ExecutionException, DaggerQueryException, InterruptedException {
        return this.commitAndRef(git);
    }

    private String commitAndRef(GitRef git) throws ExecutionException, DaggerQueryException, InterruptedException {
        var commit = git.commit();
        var reference = git.ref();
        return "%s@%s".formatted(reference, commit);
    }
}
