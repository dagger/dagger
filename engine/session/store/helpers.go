package store

import (
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/pkg/protobuf"

	imagesapi "github.com/containerd/containerd/api/services/images/v1"
	leasesapi "github.com/containerd/containerd/api/services/leases/v1"
)

func descFromProto(desc *types.Descriptor) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType:   desc.MediaType,
		Size:        desc.Size,
		Digest:      digest.Digest(desc.Digest),
		Annotations: desc.Annotations,
	}
}

func descToProto(desc *ocispec.Descriptor) *types.Descriptor {
	return &types.Descriptor{
		MediaType:   desc.MediaType,
		Size:        desc.Size,
		Digest:      desc.Digest.String(),
		Annotations: desc.Annotations,
	}
}

func imagesToProto(images []images.Image) []*imagesapi.Image {
	var imagespb []*imagesapi.Image
	for _, image := range images {
		imagespb = append(imagespb, imageToProto(&image))
	}
	return imagespb
}

func imageToProto(image *images.Image) *imagesapi.Image {
	return &imagesapi.Image{
		Name:      image.Name,
		Labels:    image.Labels,
		Target:    descToProto(&image.Target),
		CreatedAt: protobuf.ToTimestamp(image.CreatedAt),
		UpdatedAt: protobuf.ToTimestamp(image.UpdatedAt),
	}
}

func imageFromProto(imagepb *imagesapi.Image) images.Image {
	return images.Image{
		Name:      imagepb.Name,
		Labels:    imagepb.Labels,
		Target:    descFromProto(imagepb.Target),
		CreatedAt: protobuf.FromTimestamp(imagepb.CreatedAt),
		UpdatedAt: protobuf.FromTimestamp(imagepb.UpdatedAt),
	}
}

func leaseToProto(lease leases.Lease) *leasesapi.Lease {
	return &leasesapi.Lease{
		ID:        lease.ID,
		Labels:    lease.Labels,
		CreatedAt: protobuf.ToTimestamp(lease.CreatedAt),
	}
}

func resourceToProto(resource leases.Resource) *leasesapi.Resource {
	return &leasesapi.Resource{
		ID:   resource.ID,
		Type: resource.Type,
	}
}

func protoToResource(resource *leasesapi.Resource) leases.Resource {
	return leases.Resource{
		ID:   resource.ID,
		Type: resource.Type,
	}
}
