package tailscale

import (
	"context"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/tailscale/ipn"
	"github.com/sagernet/tailscale/ipn/ipnlocal"
)

const (
	loginURLTimeout = 30 * time.Second
	loginTimeout    = 10 * time.Minute
	loginGrace      = 5 * time.Second
)

type Login struct {
	url          string
	localBackend *ipnlocal.LocalBackend
	ctx          context.Context
	cancel       context.CancelFunc
}

func (n *Node) Login() (*Login, error) {
	localBackend := n.server.ExportLocalBackend()
	ctx, cancel := context.WithTimeout(n.ctx, loginTimeout)
	urlContext, urlCancel := context.WithTimeout(ctx, loginURLTimeout)
	defer urlCancel()
	var url string
	var err error
	localBackend.WatchNotifications(urlContext, 0, func() {
		err = localBackend.StartLoginInteractive(ctx)
		if err != nil {
			urlCancel()
		}
	}, func(notify *ipn.Notify) bool {
		if notify.ErrMessage != nil {
			err = E.New(*notify.ErrMessage)
			return false
		}
		if notify.BrowseToURL == nil {
			return true
		}
		url = *notify.BrowseToURL
		return false
	})
	if url == "" {
		cancel()
		if err == nil {
			err = urlContext.Err()
		}
		return nil, E.Cause(err, "tailscale: start login")
	}
	return &Login{url: url, localBackend: localBackend, ctx: ctx, cancel: cancel}, nil
}

func (l *Login) URL() string {
	return l.url
}

func (l *Login) Done() <-chan struct{} {
	return l.ctx.Done()
}

func (l *Login) Cancel() {
	time.AfterFunc(loginGrace, l.cancel)
}

func (l *Login) Wait() error {
	defer l.cancel()
	var finished bool
	l.localBackend.WatchNotifications(l.ctx, ipn.NotifyInitialState, nil, func(notify *ipn.Notify) bool {
		if notify.State == nil {
			return true
		}
		finished = *notify.State == ipn.NeedsMachineAuth || *notify.State == ipn.Running
		return !finished
	})
	if !finished {
		return l.ctx.Err()
	}
	return nil
}
