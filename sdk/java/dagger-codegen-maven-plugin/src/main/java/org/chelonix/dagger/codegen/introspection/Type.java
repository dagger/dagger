package org.chelonix.dagger.codegen.introspection;

import java.util.List;

import static java.util.Comparator.comparing;

public class Type {
    private TypeKind kind;
    private String name;
    private String description;
    private List<Field> fields;
    private List<InputValue> inputFields;
    private List<EnumValue> enumValues;

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

    public String getDescription() {
        return description;
    }

    public void setDescription(String description) {
        this.description = description.replace("\n", "<br/>");
    }

    public List<EnumValue> getEnumValues() {
        return enumValues;
    }

    public void setEnumValues(List<EnumValue> enumValues) {
        this.enumValues = enumValues;
    }

    public List<InputValue> getInputFields() {
        return inputFields;
    }

    public void setInputFields(List<InputValue> inputFields) {
        this.inputFields = inputFields == null ? null : inputFields.stream().sorted(comparing(InputValue::getName)).toList();
        // this.inputFields = inputFields;
    }

    public List<Field> getFields() {
        return fields;
    }

    public void setFields(List<Field> fields) {
        this.fields = fields == null ? null : fields.stream().sorted(comparing(Field::getName)).toList();
        //this.fields = fields;
    }

    @Override
    public String toString() {
        return "Type{" +
                "kind=" + kind +
                ", name='" + name + '\'' +
                ", description='" + description + '\'' +
                ", fields=" + fields +
                ", inputFields=" + inputFields +
                ", enumValues=" + enumValues +
                '}';
    }
}
