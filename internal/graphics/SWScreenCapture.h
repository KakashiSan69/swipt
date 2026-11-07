#import <Foundation/Foundation.h>

#import "SWFrame.h"

@class SWAccessUnit;
@class SWCancelToken;
@class SWScreenStream;

@interface SWScreenCapture : NSObject
@property (class, nonatomic, readonly) SWScreenCapture *shared;

- (BOOL)addStream:(SWScreenStream *)stream geometry:(SWScreenGeometry *)geometry error:(NSError **)error;
- (void)removeStream:(SWScreenStream *)stream;
- (SWFrame *)frameAfter:(NSInteger)previous stream:(SWScreenStream *)stream until:(NSDate *)deadline token:(SWCancelToken *)token sequence:(NSInteger *)sequence error:(NSError **)error;
- (void)requestKeyFrame;
- (NSInteger)addUnitListener:(void (^)(SWAccessUnit *unit))listener;
- (void)removeListener:(NSInteger)identifier;
- (void)onPackets:(NSData *)batch;
- (void)onEnd:(NSString *)failure;
@end
