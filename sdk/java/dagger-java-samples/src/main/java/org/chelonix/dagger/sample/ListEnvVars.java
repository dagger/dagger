package org.chelonix.dagger.sample;

import org.chelonix.dagger.client.*;

import java.util.List;

public class ListEnvVars {
    public static void main(String... args) throws Exception {
        try(Client client = Dagger.connect()) {
            List<EnvVariable> env = client.container()
                    .from("alpine")
                    .withEnvVariable("MY_VAR", "some_value")
                    .envVariables();
            for (EnvVariable var : env) {
                System.out.printf("%s = %s\n", var.name(), var.value());
            }
        }
    }
}
