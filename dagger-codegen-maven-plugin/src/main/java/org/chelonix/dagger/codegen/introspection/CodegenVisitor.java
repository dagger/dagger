package org.chelonix.dagger.codegen.introspection;

import com.squareup.javapoet.*;

import javax.lang.model.element.Modifier;
import java.io.*;
import java.nio.charset.Charset;
import java.nio.file.Path;

import static org.apache.commons.lang3.StringUtils.capitalize;

public class CodegenVisitor implements SchemaVisitor {

    private final ScalarVisitor scalarVisitor;
    private final InputVisitor inputVisitor;
    private final EnumVisitor enumVisitor;
    private final ObjectVisitor objectVisitor;

    private static MethodSpec getter(String var, TypeName type) {
        return MethodSpec.methodBuilder("get" + capitalize(var))
                .addModifiers(Modifier.PUBLIC)
                .returns(type)
                .addStatement("return this.$L", var)
                .build();
    }

    public CodegenVisitor(Schema schema, Path targetDirectory, Charset encoding) {
        this.scalarVisitor = new ScalarVisitor(schema, targetDirectory, encoding);
        this.inputVisitor = new InputVisitor(schema, targetDirectory, encoding);
        this.enumVisitor = new EnumVisitor(schema, targetDirectory, encoding);
        this.objectVisitor = new ObjectVisitor(schema, targetDirectory, encoding);
    }

    @Override
    public void visitScalar(Type type) {
        try {
            scalarVisitor.visit(type);
        } catch (IOException e) {
            throw new RuntimeException(e);
        }
    }

    @Override
    public void visitObject(Type type) {
        try {
            objectVisitor.visit(type);
        } catch (IOException e) {
            throw new RuntimeException(e);
        }
    }



    @Override
    public void visitInput(Type type) {
        try {
            inputVisitor.visit(type);
        } catch (IOException e) {
            throw new RuntimeException(e);
        }
    }

    @Override
    public void visitEnum(Type type) {
        try {
            enumVisitor.visit(type);
        } catch (IOException e) {
            throw new RuntimeException(e);
        }
    }
}
