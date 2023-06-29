package org.chelonix.dagger.sample;

import org.chelonix.dagger.client.Client;
import org.chelonix.dagger.client.Container;
import org.chelonix.dagger.client.Dagger;

import java.util.List;

public class SimpleContainer {
    public static void main(String... args) throws Exception {
        try(Client client = Dagger.connect()) {
            Container container = client.container()
                    .from("maven:3.9.2")
                    .withExec(List.of("mvn", "--version"));

            String version = container.stdout();
            System.out.println("Hello from Dagger and " + version);
        }
    }
}
