package org.chelonix.dagger.model;

import java.util.List;

/**
 * A simple key value object that represents an environment variable.
 */
public class EnvVariable {

    private QueryContext context;

    private String name;
    private String value;

    protected EnvVariable() {}

    EnvVariable(QueryContext context) {
        this.context = context;
    }

    /**
     * <p>The environment variable name.</p>
     *
     */
    public String name() {
        if (name != null) {
            return name;
        }
        QueryContext ctx = context.chain(new QueryPart("name"));
        // return ...
        return null;
    }
    /**
     * <p>The environment variable value.</p>
     *
     */
    public String value() {
        if (value != null) {
            return value;
        }
        QueryContext ctx = context.chain(new QueryPart("value"));
        // return ...
        return null;
    }

}