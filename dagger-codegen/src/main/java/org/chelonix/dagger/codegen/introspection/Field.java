package org.chelonix.dagger.codegen.introspection;

import jakarta.json.bind.annotation.JsonbProperty;

import java.util.List;

public class Field {

    private String name;
    private String description;
    @JsonbProperty("type")
    private TypeRef typeRef;
    private List<InputValue> args;
    @JsonbProperty("isDeprecated")
    private boolean deprecated; // isDeprecated
    private String DeprecationReason;

    private Type parentObject;

    public String getName() {
        return name;
    }

    public void setName(String name) {
        this.name = name;
    }

    public String getDescription() {
        return description;
    }

    public void setDescription(String description) {
        this.description = description;
    }

    public TypeRef getTypeRef() {
        return typeRef;
    }

    public void setTypeRef(TypeRef typeRef) {
        this.typeRef = typeRef;
    }

    public List<InputValue> getArgs() {
        return args;
    }

    public void setArgs(List<InputValue> args) {
        this.args = args;
    }

    public boolean isDeprecated() {
        return deprecated;
    }

    public void setDeprecated(boolean deprecated) {
        this.deprecated = deprecated;
    }

    public String getDeprecationReason() {
        return DeprecationReason;
    }

    public void setDeprecationReason(String deprecationReason) {
        DeprecationReason = deprecationReason;
    }

    public Type getParentObject() {
        return parentObject;
    }

    public void setParentObject(Type parentObject) {
        this.parentObject = parentObject;
    }

    @Override
    public String toString() {
        return "Field{" +
                "name='" + name + '\'' +
                // ", description='" + description + '\'' +
                ", args=" + args +
                ", deprecated=" + deprecated +
                // ", DeprecationReason='" + DeprecationReason + '\'' +
                '}';
    }
}
