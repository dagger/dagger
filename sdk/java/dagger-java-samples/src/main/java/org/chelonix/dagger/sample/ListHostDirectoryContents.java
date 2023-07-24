package org.chelonix.dagger.sample;

import org.chelonix.dagger.client.Client;
import org.chelonix.dagger.client.Dagger;
import org.chelonix.dagger.client.EnvVariable;

import java.util.List;

public class ListHostDirectoryContents {
    public static void main(String... args) throws Exception {
        try(Client client = Dagger.connect()) {
            List<String> entries = client.host().directory(".").entries();
            entries.stream().forEach(System.out::println);
        }
    }
}
