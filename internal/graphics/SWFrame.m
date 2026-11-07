//go:build darwin && cgo

#import "SWFrame.h"

#import <VideoToolbox/VideoToolbox.h>

#import "SWError.h"
#import "SWRaster.h"

@implementation SWFrame {
    CVPixelBufferRef _pixelBuffer;
    NSInteger _cropWidth;
    NSInteger _cropHeight;
}

- (instancetype)initWithPixelBuffer:(CVPixelBufferRef)pixelBuffer time:(double)time turns:(NSInteger)turns geometry:(SWScreenGeometry)geometry {
    self = [super init];
    if (self) {
        _pixelBuffer = CVPixelBufferRetain(pixelBuffer);
        _time = time;
        _turns = turns;
        _geometry = geometry;
        NSInteger width = CVPixelBufferGetWidth(pixelBuffer);
        NSInteger height = CVPixelBufferGetHeight(pixelBuffer);
        _cropWidth = geometry.width > 0 ? MIN(geometry.width, width) : width;
        _cropHeight = geometry.height > 0 ? MIN(geometry.height, height) : height;
    }
    return self;
}

- (void)dealloc {
    CVPixelBufferRelease(_pixelBuffer);
}

- (NSInteger)pixelWidth {
    return _turns % 2 == 0 ? _cropWidth : _cropHeight;
}

- (NSInteger)pixelHeight {
    return _turns % 2 == 0 ? _cropHeight : _cropWidth;
}

- (SWRaster *)raster:(NSError **)error {
    SWRaster *native = [[SWRaster alloc] initWithPixelBuffer:_pixelBuffer width:_cropWidth height:_cropHeight error:error];
    return native == nil ? nil : SWRasterRotatedQuarters(native, _turns, error);
}

@end
