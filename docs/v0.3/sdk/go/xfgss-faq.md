---
slug: /xfgss/faq
---

# FAQ

### What's the difference between Dagger 0.2 and 0.3?

The primary difference is that Dagger 0.3 exposes its functionality via a low-level language-agnostic API framework and provides native language SDKs that communicate with this API. This has two main benefits:

- It enables CI/CD pipeline authors to use whichever language feels natural and intuitive to them, without having to think about interoperability.
- Rather than reimplementing the same automation in every possible programming language, it only needs to be implemented once and is then reusable by all. As a result, two cooperating teams no longer necessarily have to come to consensus on a common language.

### What API framework does Dagger 0.3 use?

Dagger 0.3 uses GraphQL as its low-level language-agnostic API framework.

### What language SDKs are available for Dagger 0.3?

We currently offer a technical preview of the Go SDK. We plan to add SDKs for other languages in future.

### There's no SDK for &lt;language&gt; yet. Can I still use Dagger 0.3?

Yes. It's possible to use the Dagger API from any language that [supports GraphQL](https://github.com/chentsulin/awesome-graphql).

### Do I need to know GraphQL for Dagger 0.3?

No. You only need to know one of Dagger's supported languages to use Dagger. Communicating with the Dagger API is now as simple as calling Dagger SDK functions in the language you choose to use. The conversion of those function calls to language-agnostic API calls is performed by the Dagger SDK. You do not need to be an expert in GraphQL to use Dagger.

### Do I still need to know CUE for Dagger 0.3?

No. With Dagger 0.3, you can author your automation pipeline in Go, Typescript, Python, or even a shell script.

### Can I run Dagger 0.2 CUE plans in Dagger 0.3?

We are working on porting CUE support into Dagger 0.3 as an additional language.

### I am stuck. How can I get help?

Join us on [Discord](https://discord.com/invite/dagger-io), and ask your question in our [help forum](https://discord.com/channels/707636530424053791/1030538312508776540). Our team will be happy to help you there!
