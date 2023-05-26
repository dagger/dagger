package org.chelonix.dagger.codegen.introspection;

public class TypeRef {

    private TypeKind kind;
    private String name;
    private TypeRef ofType;

    public TypeKind getKind() {
        return kind;
    }

    public void setKind(TypeKind kind) {
        this.kind = kind;
    }

    public String getName() {
        return name;
    }

    public void setName(String name) {
        this.name = name;
    }

    public TypeRef getOfType() {
        return ofType;
    }

    public void setOfType(TypeRef ofType) {
        this.ofType = ofType;
    }

    public boolean isOptional() {
        return kind != TypeKind.NON_NULL;
    }

    public boolean isScalar() {
        TypeRef ref = this;
        if (ref.kind == TypeKind.NON_NULL) {
            ref = ref.ofType;
        }
       return ref.kind == TypeKind.SCALAR || ref.kind == TypeKind.ENUM;
    }

    public boolean isList() {
        TypeRef ref = this;
        if (ref.kind == TypeKind.NON_NULL) {
            ref = ref.ofType;
        }
        return ref.kind == TypeKind.LIST;
    }
}
