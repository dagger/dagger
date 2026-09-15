import dagger
from dagger import function, object_type

@object_type
class Test:
    @function
    def from_image_layer_compression(self, image_layer_compression: dagger.ImageLayerCompression) -> str:
        return str(image_layer_compression)

    @function
    def to_image_layer_compression(self, image_layer_compression: str) -> dagger.ImageLayerCompression:
        return dagger.ImageLayerCompression[image_layer_compression]
