package org.chelonix.dagger.sample;

import org.chelonix.dagger.client.*;

import java.util.List;

public class GetGitVersion {
    public static void main(String... args) throws Exception {
        try(Client client = Dagger.connect()) {
            Directory dir = client.git("https://github.com/dagger/dagger").tag("v0.6.2").tree();

            Container daggerImg = client.container().build(dir);

            String stdout = daggerImg.withExec(List.of("version")).stdout();
            System.out.println(stdout);
        }
    }
}
