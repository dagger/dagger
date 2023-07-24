package org.chelonix.dagger.sample;

import org.chelonix.dagger.client.Client;
import org.chelonix.dagger.client.Dagger;

import java.util.List;

public class MountHostDirectoryInContainer {
    public static void main(String... args) throws Exception {
        try(Client client = Dagger.connect()) {
            String contents = client.container().from("alpine").
                    withDirectory("/host", client.host().directory(".")).
                    withExec(List.of("ls", "/host")).
                    stdout();

            System.out.println(contents);
        }
    }
}
