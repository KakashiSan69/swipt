package device

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/swipt-automation/swipt/internal/coredevice/apps"
	"github.com/swipt-automation/swipt/internal/coredevice/instruments"
	"github.com/swipt-automation/swipt/internal/coredevice/ostrace"
)

// Enabling the notifications sent the state of every app within 40 ms. They
// report a process as Running when it starts or resumes and again when it is
// suspended (iPhone18,2, iOS 27.2 24B115), and nothing when an app that kept
// running in the background went to or left the front (iPhone18,2, iOS 27.3
// 24C5037i).
const (
	appStatesBacklog = 250 * time.Millisecond
	recentFront      = 250 * time.Millisecond
	trackingLinger   = 10 * time.Second
)

// SpringBoard posted SubstantialTransition as the app in front changed, also
// to and from the home screen, Notification Center and the app switcher, but
// not as the device locked, which posted lockstate (iPhone18,2, iOS 27.3
// 24C5037i). It came 300 ms after the transition's animation ended, 0.9 s
// after a launch (iPad17,1, iPadOS 27.3 24C5037i).
const substantialTransition = "com.apple.mobile.SubstantialTransition"

var frontNotifications = []string{substantialTransition, "com.apple.springboard.lockstate"}

// runningboardd logged "[app<BUNDLE(PERSONA)>:PID] Set darwin role to: ROLE"
// at the default level as the role of an app changed. An app took the
// UserInteractiveFocal role as SpringBoard began to bring it to the front,
// 0.9 s before SubstantialTransition, while the app it replaced kept the role
// 0.65 s longer; two apps held it at once in Split View, and an app lost it
// 1.1 to 1.4 s after the home button. Unlisted apps such as the widget
// renderers took it too (iPad17,1, iPadOS 27.3 24C5037i).
const (
	roleChange  = "] Set darwin role to: "
	focalRole   = "UserInteractiveFocal"
	roleService = "/runningboardd"
)

type focalApp struct {
	bundleID string
	since    time.Time
}

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
	bundles   map[string]string
	listed    map[string]bool
	unlisted  map[string]bool
	paths     map[uint64]string
	focal     map[uint64]focalApp
	wake      chan struct{}
	following atomic.Bool
	reading   atomic.Bool

	infoAccess sync.Mutex
	info       *instruments.DeviceInfo
	list       *instruments.DeviceInfo
}

func newForeground(c *conn) *foreground {
	return &foreground{
		conn: c, listeners: make(map[int]func(ForegroundApp)), paths: make(map[uint64]string), focal: make(map[uint64]focalApp),
		wake: make(chan struct{}, 1),
	}
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
	front, _, err := f.readShown(ctx)
	if err != nil {
		return frontApplication{}, err
	}
	return f.saw(front), nil
}

// readShown returns, besides the app it takes as the front, every listed app
// that held foregroundRunning, without telling them to saw.
func (f *foreground) readShown(ctx context.Context) (frontApplication, map[uint64]string, error) {
	if err := f.loadBundles(ctx); err != nil {
		return frontApplication{}, nil, E.Cause(err, "list the apps")
	}
	asked := time.Now()
	info, err := f.deviceInfo(ctx, &f.list)
	if err != nil {
		return frontApplication{}, nil, err
	}
	processes, err := info.RunningProcesses(ctx)
	if err != nil {
		return frontApplication{}, nil, err
	}
	var foreground []string
	for _, process := range processes {
		if process.Foreground && process.BundleID != "" {
			foreground = append(foreground, process.BundleID)
		}
	}
	if err = f.relist(ctx, foreground...); err != nil {
		return frontApplication{}, nil, err
	}
	f.access.Lock()
	current, listed := f.current, f.listed
	var springboard uint64
	shown := make(map[uint64]string)
	for _, process := range processes {
		switch {
		case process.BundleID == springboardBundleID:
			springboard = process.PID
		case process.Foreground && listed[process.BundleID]:
			shown[process.PID] = process.BundleID
		}
	}
	f.access.Unlock()
	front := frontApplication{pid: springboard, bundleID: springboardBundleID, at: asked, via: "processes"}
	if bundleID, kept := shown[current.pid]; kept {
		front.pid, front.bundleID = current.pid, bundleID
	} else {
		for pid, bundleID := range shown {
			if front.bundleID == springboardBundleID || pid < front.pid {
				front.pid, front.bundleID = pid, bundleID
			}
		}
	}
	f.conn.connection.trace(asked, "frontmost", front.bundleID+" pid "+strconv.FormatUint(front.pid, 10), time.Since(asked))
	return front, shown, nil
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

func (f *foreground) acted() {
	f.access.Lock()
	if !f.tracked {
		f.current.at = time.Time{}
	}
	f.access.Unlock()
}

func (f *foreground) poke() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
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
	f.access.Lock()
	clear(f.focal)
	f.access.Unlock()
	backlog := time.Now().Add(appStatesBacklog)
	go f.confirm(ctx)
	go f.follow(ctx)
	for {
		select {
		case state := <-pushes:
			launched := f.record(state)
			if state.At.Before(backlog) {
				continue
			}
			f.conn.events.publish(Event{Kind: EventAppState, PID: state.PID, Name: state.State})
			f.conn.connection.trace(state.At, "appState", "pid "+strconv.FormatUint(state.PID, 10)+" "+state.State+" "+f.bundleOf(state.PID), time.Since(state.At))
			if launched {
				f.launched(state)
			}
			f.poke()
		case <-pushesDone:
			f.conn.logger.Debug("watch app states: ", states.Err())
			pushes, pushesDone = nil, nil
		case <-notified:
			f.poke()
			if !f.following.Load() && f.reading.CompareAndSwap(false, true) {
				go func() {
					defer f.reading.Store(false)
					if _, err := f.read(ctx); err != nil && ctx.Err() == nil {
						f.conn.logger.Debug(E.Cause(err, "read the frontmost application"))
					}
				}()
			}
		case <-ctx.Done():
			return
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

func (f *foreground) follow(ctx context.Context) {
	for ctx.Err() == nil {
		err := f.followRoles(ctx)
		if ctx.Err() != nil {
			return
		}
		f.conn.logger.Debug(E.Cause(err, "follow the roles of processes"))
		select {
		case <-f.wake:
		case <-ctx.Done():
		}
	}
}

func (f *foreground) followRoles(ctx context.Context) error {
	processes, err := apps.Processes(ctx, f.conn.device)
	if err != nil {
		return err
	}
	service := common.Find(processes, func(it apps.Process) bool { return strings.HasSuffix(it.Executable, roleService) })
	if service.PID == 0 {
		return E.New("runningboardd is not running")
	}
	stream, err := ostrace.Dial(ctx, f.conn.device, service.PID)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { stream.Close() })
	defer stop()
	defer stream.Close()
	defer f.following.Store(false)
	for {
		entry, err := stream.Next()
		if err != nil {
			return err
		}
		if !f.following.Load() {
			front, shown, err := f.readShown(ctx)
			if err != nil {
				return err
			}
			// The roles set before the first entry are lost, so every app the
			// processes show in front holds it, in no known order, until its
			// role changes.
			f.access.Lock()
			clear(f.focal)
			for pid, bundleID := range shown {
				f.focal[pid] = focalApp{bundleID: bundleID}
			}
			f.access.Unlock()
			f.saw(front)
			f.conn.connection.trace(entry.At, "roles", "live with "+strconv.Itoa(len(shown))+" apps in front, "+front.bundleID+" pid "+strconv.FormatUint(front.pid, 10)+" first", time.Since(entry.At))
			f.following.Store(true)
		}
		f.role(ctx, entry)
	}
}

func (f *foreground) role(ctx context.Context, entry ostrace.Entry) {
	head, found := strings.CutPrefix(entry.Message, "[app<")
	head, role, changed := strings.Cut(head, roleChange)
	end := strings.IndexAny(head, "(>")
	_, pidText, numbered := strings.Cut(head, ">:")
	pid, err := strconv.ParseUint(pidText, 10, 64)
	if !found || !changed || !numbered || end <= 0 || err != nil {
		return
	}
	at, bundleID := entry.At, head[:end]
	f.conn.connection.trace(at, "role", bundleID+" pid "+pidText+" "+role, time.Since(at))
	if role == focalRole {
		if err = f.relist(ctx, bundleID); err != nil {
			if ctx.Err() == nil {
				f.conn.logger.Debug(E.Cause(err, "list the apps"))
			}
		}
	}
	f.access.Lock()
	if role == focalRole {
		f.focal[pid] = focalApp{bundleID: bundleID, since: at}
	} else {
		delete(f.focal, pid)
	}
	front := frontApplication{bundleID: springboardBundleID, at: at, via: "role"}
	var latest time.Time
	for it, app := range f.focal {
		if !f.listed[app.bundleID] {
			continue
		}
		if front.pid == 0 || app.since.After(latest) || app.since.Equal(latest) && it == f.current.pid {
			latest, front.pid, front.bundleID = app.since, it, app.bundleID
		}
	}
	f.access.Unlock()
	if front.pid == 0 {
		if front.pid, err = f.conn.springboardPID(ctx); err != nil {
			if ctx.Err() == nil {
				f.conn.logger.Debug(E.Cause(err, "find SpringBoard"))
			}
			return
		}
	}
	f.saw(front)
}

func (f *foreground) loadBundles(ctx context.Context) error {
	f.access.Lock()
	loaded := f.bundles != nil
	f.access.Unlock()
	if loaded {
		return nil
	}
	return f.listApps(ctx, nil)
}

// relist lists the apps again when an app that is neither listed nor known to
// be unlisted comes to the front, as one installed after the apps were listed
// does.
func (f *foreground) relist(ctx context.Context, bundleIDs ...string) error {
	f.access.Lock()
	unknown := slices.DeleteFunc(slices.Clone(bundleIDs), func(it string) bool { return f.listed[it] || f.unlisted[it] })
	f.access.Unlock()
	if len(unknown) == 0 {
		return nil
	}
	return f.listApps(ctx, unknown)
}

func (f *foreground) listApps(ctx context.Context, unknown []string) error {
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
	defer f.access.Unlock()
	f.bundles, f.listed = bundles, listed
	if f.unlisted == nil {
		f.unlisted = make(map[string]bool)
	}
	for _, bundleID := range unknown {
		if !listed[bundleID] {
			f.unlisted[bundleID] = true
		}
	}
	return nil
}

func (f *foreground) record(state instruments.AppState) (launched bool) {
	f.access.Lock()
	defer f.access.Unlock()
	_, known := f.paths[state.PID]
	if state.State == "Terminated" {
		delete(f.paths, state.PID)
		delete(f.focal, state.PID)
		return false
	}
	if state.Path != "" {
		f.paths[state.PID] = state.Path
	}
	return state.State == "Running" && !known && f.bundles[state.Path] != ""
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
