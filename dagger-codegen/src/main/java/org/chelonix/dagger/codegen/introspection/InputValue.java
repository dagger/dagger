package org.chelonix.dagger.codegen.introspection;

public class InputValue {

    private String name;
    private String Description;
    private String defaultValue; // isDeprecated
    private TypeRef type;

    public String getName() {
        return name;
    }

    public void setName(String name) {
        this.name = name;
    }

    public String getDescription() {
        return Description;
    }

    public void setDescription(String description) {
        Description = description;
    }

    public String getDefaultValue() {
        return defaultValue;
    }

    public void setDefaultValue(String defaultValue) {
        this.defaultValue = defaultValue;
    }

    public TypeRef getType() {
        return type;
    }

    public void setType(TypeRef type) {
        this.type = type;
    }

    @Override
    public String toString() {
        return "InputValue{" +
                "name='" + name + '\'' +
                // ", Description='" + Description + '\'' +
                ", defaultValue='" + defaultValue + '\'' +
                ", type=" + type +
                '}';
    }
}
