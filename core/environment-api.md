```graphql
type LLM {
  model: String!
  withPrompt(prompt: String!): LLM!
  history: [LLMMessage!]!
  lastReply(): String!

  withEnvironment(Environment!): LLM!
  environment: Environment
}
```

```graphql
type Environment {
  with[Type]Binding(key: String!, value: [Type], overwrite: Bool, overwriteType: Bool): Environment!
  bindings: [Binding!]
  binding(key: String!): Binding
  encode: File!
}
```

```graphql
type Binding {
  key: String!
  as[Type]: [Type]!
}
```
