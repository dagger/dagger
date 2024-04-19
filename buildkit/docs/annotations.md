# Image annotations

Buildkit supports attaching [OCI annotations](https://github.com/opencontainers/image-spec/blob/main/annotations.md)
to its built image manifests and indexes. These annotations can be used to
attach additional metadata to a built image, which may not be appropriate to
store in the image content itself.

Annotations are similar to, but not a replacement for image labels. Annotations
can be attached at almost every level of the resulting image output, while
labels can be only included in the image configuration object. Additionally,
unless overridden, image labels are inherited by other images that use the
image as a base.

Annotations support multiple pre-defined annotation keys which you can use, or
you can also create your own.

To build an image with annotations, you can use the `image` or `oci` (and
related) exporter types, along with the `annotation.*` option.

For example, to attach a human-readable title to your image, you can use the
following buildctl invocation:

    buildctl build ... \
        --opt platform=amd64,arm64 \
        --output "type=image,name=target,annotation.org.opencontainers.image.title=Target"
        
This annotation will be added to each built image manifest, so each platform
you built for (in the above, `amd64` and `arm64`) will get a copy of the annotation.

You want to allow different annotations for different platforms, e.g. maybe you
want to provide a different documentation URL per manifest. You can do this
with platform specific annotations, using the `annotation[<platform>].*` syntax
like so:

    buildctl build ... \
        --opt platform=amd64,arm64 \
        --output "type=image,name=target,annotation[linux/amd64].org.opencontainers.image.url=https://example.com/amd64,annotation[linux/arm64].org.opencontainers.image.url=https://example.com/arm64"
        
Buildkit also allows you to finely control the exact destination where the
annotation will be written to using the syntax `annotation-<type>.*`. You can
write to the following `<type>`s:

- The `manifest` (the default, as above)
- The `manifest-descriptor`
    - This adds the annotation into the image index's descriptor for the manifest
- The `index`
    - This adds the annotation into the image index root
    - If the exporter does not produce an image index, the build will fail.
- The `index-descriptor`
    - This adds the annotation into the OCI layout's descriptor for the index
    - If the exporter does not produce an image index, the build will fail.

For example, if you want to add the annotation at the image index level, so
that the annotation is shared between all architectures, you can instead:

    buildctl build ... \
        --opt platform=amd64,arm64 \
        --output "type=image,name=target,annotation-index.org.opencontainers.image.title=Target Image"
