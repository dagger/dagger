package image

import v1 "github.com/moby/docker-image-spec/specs-go/v1"

// HealthConfig holds configuration settings for the HEALTHCHECK feature.
//
// Deprecated: use [v1.HealthcheckConfig].
type HealthConfig = v1.HealthcheckConfig

// ImageConfig is a docker compatible config for an image
//
// Deprecated: use [v1.DockerOCIImageConfig].
type ImageConfig = v1.DockerOCIImageConfig

// Image is the JSON structure which describes some basic information about the image.
// This provides the `application/vnd.oci.image.config.v1+json` mediatype when marshalled to JSON.
//
// Deprecated: use [v1.DockerOCIImage].
type Image = v1.DockerOCIImage
