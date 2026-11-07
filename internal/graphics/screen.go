//go:build darwin && cgo

package graphics

/*
#include <stdlib.h>
#include "graphics.h"
*/
import "C"

import (
	"context"
	"runtime/cgo"
	"time"
	"unsafe"

	"github.com/swipt-automation/swipt/internal/automation"
	"github.com/swipt-automation/swipt/internal/device"
)

const screenOpenLimit = 20 * time.Second

func screenStreamOptions(mode *C.char, maximumBitrate C.uint64_t) device.ScreenStreamOptions {
	return device.ScreenStreamOptions{Mode: device.ScreenMode(C.GoString(mode)), MaximumBitrate: uint64(maximumBitrate)}
}

//export swiptGraphicsOpenScreenStream
func swiptGraphicsOpenScreenStream(mode *C.char, maximumBitrate C.uint64_t) C.swipt_graphics_screen_stream {
	ctx, cancel := context.WithTimeout(context.Background(), screenOpenLimit)
	defer cancel()
	stream, err := current.Load().options.Connection.OpenScreenStream(ctx, screenStreamOptions(mode, maximumBitrate), deliverScreenStream, endScreenStream)
	if err != nil {
		return C.swipt_graphics_screen_stream{failure: C.CString(automation.FailureOf(err))}
	}
	info := stream.Info()
	return C.swipt_graphics_screen_stream{stream: C.uintptr_t(cgo.NewHandle(stream)), width: C.int64_t(info.Width), height: C.int64_t(info.Height), scale: C.double(info.Scale)}
}

//export swiptGraphicsConfigureScreenStream
func swiptGraphicsConfigureScreenStream(handle C.uintptr_t, mode *C.char, maximumBitrate C.uint64_t) {
	cgo.Handle(handle).Value().(*device.ScreenStream).Configure(screenStreamOptions(mode, maximumBitrate))
}

//export swiptGraphicsRequestScreenKeyFrame
func swiptGraphicsRequestScreenKeyFrame(handle C.uintptr_t) {
	cgo.Handle(handle).Value().(*device.ScreenStream).RequestKeyFrame()
}

//export swiptGraphicsCloseScreenStream
func swiptGraphicsCloseScreenStream(handle C.uintptr_t) {
	cgo.Handle(handle).Value().(*device.ScreenStream).Close()
}

func deliverScreenStream(batch []byte) {
	if len(batch) > 0 {
		C.swipt_graphics_screen_stream_packets(unsafe.Pointer(&batch[0]), C.size_t(len(batch)))
	}
}

func endScreenStream(err error) {
	failure := C.CString(automation.FailureOf(err))
	defer C.free(unsafe.Pointer(failure))
	C.swipt_graphics_screen_stream_ended(failure)
}
