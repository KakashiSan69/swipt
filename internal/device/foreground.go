package device

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/swipt-automation/swipt/internal/coredevice/apps"
	"github.com/swipt-automation/swipt/internal/coredevice/instruments"
)

// Enabling the notifications sent the state of every app within 40 ms. They
// report a process as Running when it starts or resumes and again when it is
// suspended (iPhone18,2, iOS 27.2 24B115), and nothing when an app that kept
// running in the background went to or left the front (iPhone18,2, iOS 27.3
// 24C5037i).
const (
	appStatesBacklog = 250 * time.Millisecond
	recentFront      = 250 * time.Millisecond
	frontBurst       = 1200 * time.Millisecond
	trackingLinger   = 10 * time.Second
	frontProbe       = 3 * time.Second
)

// SpringBoard posted SubstantialTransition as the app in front changed, also
// to and from the home screen, Notification Center and the app switcher, but
// not as the device locked, which posted lockstate (iPhone18,2, iOS 27.3
// 24C5037i).
const substantialTransition = "com.apple.mobile.SubstantialTransition"

var frontNotifications = []string{substantialTransition, "com.apple.springboard.lockstate"}

type ForegroundApp struct {
	BundleID string
	PID      uint64
	Launched bool
	At       time.Time
}

type frontApplication struct {
	pid      uint64
	bundleID string
	at       time.Time
	via      string
}

type foreground struct {
	conn *conn

	access    sync.Mutex
	current   frontApplication
	tracked   bool
	listeners map[int]func(ForegroundApp)
	next      int
	stop      context.CancelFunc
	stopped   chan struct{}
	linger    *time.Timer
	until     time.Time
	kick      chan struct{}
	bundles   map[string]string
	listed    map[string]bool
	paths     map[uint64]string

	infoAccess sync.Mutex
	info       *instruments.DeviceInfo
	list       *instruments.DeviceInfo
}

func newForeground(c *conn) *foreground {
	return &foreground{conn: c, listeners: make(map[int]func(ForegroundApp)), kick: make(chan struct{}, 1), paths: make(map[uint64]string)}
}

func (f *foreground) get(ctx context.Context) (frontApplication, error) {
	f.access.Lock()
	current, tracked := f.current, f.tracked
	f.access.Unlock()
	if tracked && current.bundleID != "" || time.Since(current.at) < recentFront {
		return current, nil
	}
	return f.read(ctx)
}

// SpringBoard held foregroundRunning throughout, and so did unlisted system
// apps such as InputUI; an app held it in the app switcher and under Control
// Center, but not under Notification Center, and both apps held it while
// one replaced the other (iPhone18,2, iOS 27.3 24C5037i).
func (f *foreground) read(ctx context.Context) (frontApplication, error) {
	if err := f.loadBundles(ctx); err != nil {
		return frontApplication{}, E.Cause(err, "list the apps")
	}
	asked := time.Now()
	info, err := f.deviceInfo(ctx, &f.list)
	if err != nil {
		return frontApplication{}, err
	}
	processes, err := info.RunningProcesses(ctx)
	if err != nil {
		return frontApplication{}, err
	}
	f.access.Lock()
	current, listed := f.current, f.listed
	f.access.Unlock()
	var springboard uint64
	var shown []instruments.RunningProcess
	for _, process := range processes {
		switch {
		case process.BundleID == springboardBundleID:
			springboard = process.PID
		case process.Foreground && listed[process.BundleID]:
			shown = append(shown, process)
		}
	}
	front := frontApplication{pid: springboard, bundleID: springboardBundleID, at: asked, via: "processes"}
	if len(shown) > 0 {
		chosen := common.Find(shown, func(it instruments.RunningProcess) bool { return it.PID == current.pid })
		if chosen.PID == 0 {
			chosen = shown[0]
		}
		front.pid, front.bundleID = chosen.PID, chosen.BundleID
	}
	f.conn.connection.trace(asked, "frontmost", front.bundleID+" pid "+strconv.FormatUint(front.pid, 10), time.Since(asked))
	return f.saw(front), nil
}

func (f *foreground) deviceInfo(ctx context.Context, slot **instruments.DeviceInfo) (*instruments.DeviceInfo, error) {
	f.infoAccess.Lock()
	defer f.infoAccess.Unlock()
	if *slot != nil && (*slot).Err() != nil {
		(*slot).Close()
		*slot = nil
	}
	if *slot == nil {
		info, err := instruments.DialDeviceInfo(ctx, f.conn.ctx, f.conn.device)
		if err != nil {
			return nil, err
		}
		*slot = info
	}
	return *slot, nil
}

func (f *foreground) foregroundProcesses(ctx context.Context, pids []uint64) ([]uint64, error) {
	info, err := f.deviceInfo(ctx, &f.info)
	if err != nil {
		return nil, err
	}
	var shown []uint64
	for _, pid := range pids {
		running, err := info.Foreground(ctx, pid)
		if err != nil {
			info.Close()
			return nil, err
		}
		if running {
			shown = append(shown, pid)
		}
	}
	return shown, nil
}

func (f *foreground) closeInfo() {
	f.infoAccess.Lock()
	defer f.infoAccess.Unlock()
	for _, info := range []*instruments.DeviceInfo{f.info, f.list} {
		if info != nil {
			info.Close()
		}
	}
	f.info, f.list = nil, nil
}

func (f *foreground) probeFront(ctx context.Context, pid uint64, bundleID string) {
	if bundleID == "" || bundleID == springboardBundleID {
		return
	}
	deadline := time.Now().Add(frontProbe)
	for time.Now().Before(deadline) {
		f.access.Lock()
		current := f.current.pid
		f.access.Unlock()
		if current == pid {
			return
		}
		asked := time.Now()
		shown, err := f.foregroundProcesses(ctx, []uint64{pid})
		if err != nil {
			if ctx.Err() == nil {
				f.conn.logger.Debug(E.Cause(err, "read the foreground state of process ", pid))
			}
			return
		}
		if len(shown) > 0 {
			f.saw(frontApplication{pid: pid, bundleID: bundleID, at: asked, via: "instruments"})
			return
		}
		if sleep(ctx, pollFirst) != nil {
			return
		}
	}
}

func (f *foreground) bundleOf(pid uint64) string {
	f.access.Lock()
	defer f.access.Unlock()
	if f.bundles == nil {
		return ""
	}
	return f.bundles[f.paths[pid]]
}

func (f *foreground) saw(front frontApplication) frontApplication {
	f.access.Lock()
	defer f.access.Unlock()
	if !front.at.After(f.current.at) {
		return f.current
	}
	pid := front.pid
	changed := pid != f.current.pid || front.bundleID != f.current.bundleID
	f.current = front
	if !changed {
		return front
	}
	f.conn.connection.trace(front.at, "foreground", front.bundleID+" pid "+strconv.FormatUint(pid, 10)+" via "+front.via, time.Since(front.at))
	app := ForegroundApp{BundleID: front.bundleID, PID: pid, At: front.at}
	f.conn.events.publish(Event{Kind: EventForeground, PID: app.PID, BundleID: app.BundleID})
	for _, listener := range f.listeners {
		listener(app)
	}
	return front
}

func (f *foreground) watch(listener func(ForegroundApp)) (cancel func()) {
	f.access.Lock()
	f.next++
	id := f.next
	f.listeners[id] = listener
	if f.linger != nil {
		f.linger.Stop()
		f.linger = nil
	}
	if f.stop == nil {
		ctx, stop := context.WithCancel(f.conn.ctx)
		f.stop, f.stopped = stop, make(chan struct{})
		go f.track(ctx, f.stopped)
	}
	f.access.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			f.access.Lock()
			defer f.access.Unlock()
			delete(f.listeners, id)
			if len(f.listeners) > 0 || f.stop == nil {
				return
			}
			var linger *time.Timer
			linger = time.AfterFunc(trackingLinger, func() {
				f.access.Lock()
				defer f.access.Unlock()
				if f.linger == linger && len(f.listeners) == 0 && f.stop != nil {
					f.stop()
					f.stop, f.tracked, f.linger = nil, false, nil
				}
			})
			f.linger = linger
		})
	}
}

func (f *foreground) trigger(window time.Duration) {
	f.access.Lock()
	if until := time.Now().Add(window); until.After(f.until) {
		f.until = until
	}
	f.access.Unlock()
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

func (f *foreground) acted() {
	f.access.Lock()
	if !f.tracked {
		f.current.at = time.Time{}
	}
	f.access.Unlock()
}

func (f *foreground) track(ctx context.Context, stopped chan struct{}) {
	defer close(stopped)
	_, _, unwatch := f.conn.darwin.watch()
	defer unwatch()
	notified, unsubscribe := f.conn.events.subscribe(func(event Event) bool {
		return event.Kind == EventDevice && slices.Contains(frontNotifications, event.Name)
	})
	defer unsubscribe()
	if err := f.loadBundles(ctx); err != nil && ctx.Err() == nil {
		f.conn.logger.Debug(E.Cause(err, "list apps"))
	}
	var pushes <-chan instruments.AppState
	var pushesDone <-chan struct{}
	states, err := instruments.DialAppStates(ctx, ctx, f.conn.device)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		f.conn.logger.Debug(E.Cause(err, "watch app states"))
	} else {
		defer states.Close()
		pushes, pushesDone = states.States(), states.Done()
	}
	backlog := time.Now().Add(appStatesBacklog)
	confirm := time.NewTimer(time.Hour)
	confirm.Stop()
	defer confirm.Stop()
	confirmed := make(chan struct{}, 1)
	confirming := true
	go func() {
		f.confirm(ctx)
		confirmed <- struct{}{}
	}()
	var delay time.Duration
	for {
		select {
		case <-confirmed:
			confirming = false
			f.access.Lock()
			until := f.until
			f.access.Unlock()
			if time.Now().Before(until) {
				confirm.Reset(min(max(delay*2, pollFirst), pollLongest))
			}
			continue
		case state := <-pushes:
			relevant, launched := f.record(state)
			if state.At.Before(backlog) {
				continue
			}
			f.conn.events.publish(Event{Kind: EventAppState, PID: state.PID, Name: state.State})
			f.conn.connection.trace(state.At, "appState", "pid "+strconv.FormatUint(state.PID, 10)+" "+state.State+" "+f.bundleOf(state.PID), time.Since(state.At))
			if launched {
				f.launched(state)
			}
			if relevant {
				if state.State == "Running" {
					go f.probeFront(ctx, state.PID, f.bundleOf(state.PID))
				}
				f.trigger(frontBurst)
			}
			continue
		case <-pushesDone:
			f.conn.logger.Debug("watch app states: ", states.Err())
			pushes, pushesDone = nil, nil
			continue
		case <-notified:
			f.trigger(frontBurst)
			continue
		case <-f.kick:
			delay = 0
		case <-confirm.C:
		case <-ctx.Done():
			return
		}
		if confirming {
			continue
		}
		f.access.Lock()
		until := f.until
		f.access.Unlock()
		confirming = true
		go func() {
			f.confirm(ctx)
			confirmed <- struct{}{}
		}()
		if left := time.Until(until); left > 0 {
			delay = min(max(delay*2, pollFirst), pollLongest)
			confirm.Reset(min(delay, left))
		}
	}
}

func (f *foreground) confirm(ctx context.Context) {
	if _, err := f.read(ctx); err != nil {
		if ctx.Err() == nil {
			f.conn.logger.Debug(E.Cause(err, "read the frontmost application"))
		}
		return
	}
	f.access.Lock()
	f.tracked = f.stop != nil
	f.access.Unlock()
}

func (f *foreground) loadBundles(ctx context.Context) error {
	f.access.Lock()
	loaded := f.bundles != nil
	f.access.Unlock()
	if loaded {
		return nil
	}
	installed, err := apps.List(ctx, f.conn.device)
	if err != nil {
		return err
	}
	bundles := make(map[string]string)
	listed := make(map[string]bool)
	for _, app := range installed {
		if app.Path != "" {
			bundles[app.Path] = app.BundleID
		}
		listed[app.BundleID] = true
	}
	f.access.Lock()
	f.bundles, f.listed = bundles, listed
	f.access.Unlock()
	return nil
}

func (f *foreground) record(state instruments.AppState) (relevant, launched bool) {
	f.access.Lock()
	defer f.access.Unlock()
	_, known := f.paths[state.PID]
	switch {
	case state.State == "Terminated":
		delete(f.paths, state.PID)
	case state.Path != "":
		f.paths[state.PID] = state.Path
	}
	if state.PID == f.current.pid {
		return true, false
	}
	if state.State != "Running" || f.bundles[state.Path] == "" {
		return false, false
	}
	return true, !known
}

func (f *foreground) launched(state instruments.AppState) {
	f.access.Lock()
	defer f.access.Unlock()
	app := ForegroundApp{BundleID: f.bundles[state.Path], PID: state.PID, Launched: true, At: time.Now()}
	f.conn.connection.trace(app.At, "launch", app.BundleID+" pid "+strconv.FormatUint(app.PID, 10), 0)
	for _, listener := range f.listeners {
		listener(app)
	}
}

func (f *foreground) waitFor(ctx context.Context, bundleID string, deadline time.Time) (string, error) {
	changed := make(chan struct{}, 1)
	cancel := f.watch(func(ForegroundApp) {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	defer cancel()
	for {
		front, err := f.get(ctx)
		if err != nil || front.bundleID == bundleID {
			return front.bundleID, err
		}
		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-changed:
		case <-timer.C:
			return front.bundleID, nil
		case <-ctx.Done():
			timer.Stop()
			return front.bundleID, ctx.Err()
		}
		timer.Stop()
	}
}

func (f *foreground) close() {
	f.access.Lock()
	stop, stopped := f.stop, f.stopped
	f.stop = nil
	if f.linger != nil {
		f.linger.Stop()
		f.linger = nil
	}
	f.access.Unlock()
	if stop != nil {
		stop()
		<-stopped
	}
	f.closeInfo()
}

type AppEvent struct {
	Kind     string
	BundleID string
	PID      uint64
	Time     time.Time
}

func (p *Phone) WatchApps(ctx context.Context, emit func(AppEvent)) error {
	var last ForegroundApp
	for {
		c, operation, done, err := p.begin(ctx)
		if err != nil {
			return err
		}
		var access sync.Mutex
		var queued []ForegroundApp
		changed := make(chan struct{}, 1)
		cancel := c.front.watch(func(app ForegroundApp) {
			access.Lock()
			queued = append(queued, app)
			access.Unlock()
			select {
			case changed <- struct{}{}:
			default:
			}
		})
		if last.PID == 0 {
			if front, err := c.front.get(operation); err == nil {
				access.Lock()
				if len(queued) == 0 {
					last = ForegroundApp{BundleID: front.bundleID, PID: front.pid}
				}
				access.Unlock()
			}
		}
		for operation.Err() == nil {
			select {
			case <-changed:
			case <-operation.Done():
				continue
			}
			access.Lock()
			arrived := queued
			queued = nil
			access.Unlock()
			for _, app := range arrived {
				if app.Launched {
					emit(AppEvent{Kind: "launch", BundleID: app.BundleID, PID: app.PID, Time: app.At})
					continue
				}
				if app.PID == last.PID {
					continue
				}
				if last.PID != 0 {
					emit(AppEvent{Kind: "background", BundleID: last.BundleID, PID: last.PID, Time: app.At})
				}
				emit(AppEvent{Kind: "foreground", BundleID: app.BundleID, PID: app.PID, Time: app.At})
				last = app
			}
		}
		cancel()
		done()
		if err = ctx.Err(); err != nil {
			return err
		}
	}
}
