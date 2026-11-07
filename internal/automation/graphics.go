//go:build darwin && cgo

package automation

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/swipt-automation/swipt/internal/jsc"
)

type Graphics interface {
	Entry() uintptr
	Run(engine uint64, call int64, context *GraphicsCall) (json.RawMessage, error)
	Watch(engine uint64, call int64, context *GraphicsCall, publish func(any)) error
}

const (
	graphicsInstall int32 = iota
	graphicsUninstall
)

var (
	errNoGraphics   = E.New("graphics: this platform has no graphics")
	errStreamEnded  = E.New("the stream ended")
	graphicsEngines atomic.Uint64
)

var readOperations = []string{
	"device.info", "device.state", "device.installedApplications", "device.applicationIcon", "device.pasteboard",
	"accessibility.snapshot", "accessibility.find", "accessibility.changed",
}

type GraphicsCall struct {
	ctx  context.Context
	call *operation
}

func (c *GraphicsCall) Context() context.Context {
	return c.ctx
}

func (c *GraphicsCall) Screenshot() ([]byte, float64, error) {
	phone, err := c.call.screen(c.ctx)
	if err != nil {
		return nil, 0, err
	}
	attempted := time.Now()
	data, scale, err := phone.Capture(c.ctx)
	if c.call.dropped(c.ctx, err, attempted) {
		attempted = time.Now()
		data, scale, err = phone.Capture(c.ctx)
	}
	if c.call.dropped(c.ctx, err, attempted) {
		err = c.call.droppedError(err, "during the screenshot")
	}
	return data, scale, err
}

func (c *GraphicsCall) Authorize(name string, write bool) error {
	e := c.call.engine
	roots, where := []string{filepath.Join(e.swiplet.Directory, DataFolder), e.caches}, "the data or cache directory"
	if !write {
		roots, where = append(roots, filepath.Join(e.swiplet.Directory, AssetsFolder)), "the data, cache or assets directory"
	}
	resolved, err := resolvePath(filepath.Clean(name))
	if err != nil {
		return E.Cause(err, "graphics: resolve ", strconv.Quote(name))
	}
	if filepath.IsAbs(name) {
		for _, root := range common.FilterNotDefault(roots) {
			base, err := resolvePath(root)
			if err != nil {
				continue
			}
			if relative, err := filepath.Rel(base, resolved); err == nil && relative != "." && filepath.IsLocal(relative) {
				return nil
			}
		}
	}
	return E.Cause1(ErrNotAllowed, E.New("graphics: ", strconv.Quote(name), " is outside ", where))
}

func resolvePath(name string) (string, error) {
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(name)
		if err == nil {
			slices.Reverse(missing)
			return filepath.Join(append([]string{resolved}, missing...)...), nil
		}
		parent := filepath.Dir(name)
		if _, statErr := os.Lstat(name); statErr == nil || !errors.Is(err, fs.ErrNotExist) || parent == name {
			return "", err
		}
		missing = append(missing, filepath.Base(name))
		name = parent
	}
}

func (e *engine) installGraphics(native jsc.Value) {
	graphics := e.host.options.Graphics
	if graphics == nil {
		return
	}
	e.graphics = graphicsEngines.Add(1)
	e.context.RunNative(graphics.Entry(), graphicsInstall, native, e.graphics)
}

func (e *engine) uninstallGraphics() {
	graphics := e.host.options.Graphics
	if graphics == nil || e.graphics == 0 {
		return
	}
	e.context.RunNative(graphics.Entry(), graphicsUninstall, jsc.Value{}, e.graphics)
	e.graphics = 0
}

func graphicsRun(ctx context.Context, call *operation) (any, error) {
	var id int64
	if err := call.decode(1, &id); err != nil {
		return nil, err
	}
	graphics := call.engine.host.options.Graphics
	if graphics == nil {
		return nil, errNoGraphics
	}
	return graphics.Run(call.engine.graphics, id, &GraphicsCall{ctx: ctx, call: call})
}

func (e *engine) watchGraphics(ctx context.Context, call *operation, publish func(any, error), ended func(error)) error {
	var id int64
	if err := call.decode(1, &id); err != nil {
		return err
	}
	graphics := e.host.options.Graphics
	if graphics == nil {
		return errNoGraphics
	}
	go func() {
		err := graphics.Watch(e.graphics, id, &GraphicsCall{ctx: ctx, call: call}, func(value any) { publish(value, nil) })
		if err == nil {
			err = errStreamEnded
		}
		ended(err)
	}()
	return nil
}
