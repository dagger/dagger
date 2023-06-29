package org.chelonix.dagger.codegen.introspection;

import java.io.IOException;

public interface SchemaVisitor {

    void visitScalar(Type type);

    void visitObject(Type type);

    void visitInput(Type type);

    void visitEnum(Type type);
}
