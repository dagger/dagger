package org.chelonix.dagger;

import org.chelonix.dagger.codegen.introspection.*;

import java.io.*;

public class Main {
    public static void main(String[] args) throws Exception {
        InputStream in = Main.class.getClassLoader().getResourceAsStream("introspection.json");
        Schema schema = Schema.initialize(in);
        //System.out.println(schema);

        schema.visit(new CodegenVisitor(name -> {
            System.out.println(String.format("=== %s ===", name));
            return new OutputStreamWriter(new FilterOutputStream(System.out) {
                @Override
                public void close() throws IOException {
                    flush();
                }
            });
        }));
    }
}
