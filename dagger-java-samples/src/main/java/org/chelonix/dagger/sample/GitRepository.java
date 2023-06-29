package org.chelonix.dagger.sample;

import org.chelonix.dagger.client.Client;
import org.chelonix.dagger.client.Container;
import org.chelonix.dagger.client.Dagger;

import java.io.BufferedReader;
import java.io.StringReader;
import java.util.List;

public class GitRepository {
    public static void main(String... args) throws Exception {
        try(Client client = Dagger.connect()) {
            String readme = client.git("https://github.com/dagger/dagger")
                    .tag("v0.3.0")
                    .tree()
                    .file("README.md")
                    .contents();

            System.out.println(new BufferedReader(new StringReader(readme)).readLine());

            // Output: ## What is Dagger?
        }
    }
}