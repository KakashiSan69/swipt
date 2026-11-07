//go:build darwin && cgo

#import "SWGoBridge.h"

#include "_cgo_export.h"

#import "SWError.h"

static char *SWCString(NSString *text) {
    return (char *)text.UTF8String;
}

static NSError *SWTakeFailure(char *failure) {
    NSError *error = SWErrorFromFailure([NSString stringWithUTF8String:failure]);
    free(failure);
    return error;
}

@implementation SWGoObject

- (instancetype)initWithHandle:(uintptr_t)handle {
    self = [super init];
    if (self) {
        _handle = handle;
    }
    return self;
}

- (void)dealloc {
    swiptGraphicsRelease(_handle);
}

@end

@implementation SWCallContext

- (BOOL)cancelled {
    return swiptGraphicsCancelled(self.handle);
}

- (BOOL)authorize:(NSString *)path write:(BOOL)write error:(NSError **)error {
    char *failure = swiptGraphicsAuthorize(self.handle, SWCString(path), write);
    if (failure == NULL) {
        return YES;
    }
    SWFailWith(error, SWTakeFailure(failure));
    return NO;
}

- (NSData *)screenshotWithScale:(double *)scale error:(NSError **)error {
    swipt_graphics_screenshot shot = swiptGraphicsScreenshot(self.handle);
    if (shot.failure != NULL) {
        SWFailWith(error, SWTakeFailure(shot.failure));
        return nil;
    }
    *scale = shot.scale;
    return [NSData dataWithBytesNoCopy:shot.image length:shot.length freeWhenDone:YES];
}

- (void)publish:(NSString *)value {
    swiptGraphicsPublish(self.handle, SWCString(value));
}

@end

@implementation SWGoScreenStream

+ (instancetype)openWithMode:(NSString *)mode maximumBitrate:(uint64_t)maximumBitrate error:(NSError **)error {
    swipt_graphics_screen_stream screen = swiptGraphicsOpenScreenStream(SWCString(mode), maximumBitrate);
    if (screen.failure != NULL) {
        SWFailWith(error, SWTakeFailure(screen.failure));
        return nil;
    }
    SWGoScreenStream *stream = [[self alloc] initWithHandle:screen.stream];
    stream->_width = (NSInteger)screen.width;
    stream->_height = (NSInteger)screen.height;
    stream->_scale = MAX(screen.scale, 1);
    return stream;
}

- (void)configureMode:(NSString *)mode maximumBitrate:(uint64_t)maximumBitrate {
    swiptGraphicsConfigureScreenStream(self.handle, SWCString(mode), maximumBitrate);
}

- (void)requestKeyFrame {
    swiptGraphicsRequestScreenKeyFrame(self.handle);
}

- (void)close {
    swiptGraphicsCloseScreenStream(self.handle);
}

@end

void SWLogError(NSString *message) {
    swiptGraphicsLogError(SWCString(message));
}

void SWReportRecordingFailure(NSString *message) {
    swiptGraphicsRecordingFailed(SWCString(message));
}
