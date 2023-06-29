package org.chelonix.dagger.sample;

import org.chelonix.dagger.client.Client;
import org.chelonix.dagger.client.Dagger;

import java.util.List;

public class GetDaggerWebsite {
    public static void main(String... args) throws Exception {
        try(Client client = Dagger.connect()) {
            String output = client.pipeline("test")
                    .container()
                    .from("alpine")
                    .withExec(List.of("apk", "add", "curl"))
                    .withExec(List.of("curl", "https://dagger.io"))
                    .stdout();

            System.out.println(output.substring(0, 300));
        }
    }
}
