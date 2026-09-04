package main

import (
	"context"
	"sync"
	"time"

	"freesurf/internal/store"
	"freesurf/internal/subs"
)

// refreshTimeout bounds one subscription fetch.
const refreshTimeout = 45 * time.Second

// refresher keeps subscription servers up to date on the interval from settings.
type refresher struct {
	fetch func(context.Context, string) (string, error)
	save  func(*store.Server, []store.Node) error
	emit  func(string, ...any)

	running sync.Mutex // one pass at a time
	reset   chan struct{}
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
	once    sync.Once
}

func newRefresher(
	fetch func(context.Context, string) (string, error),
	save func(*store.Server, []store.Node) error,
	emit func(string, ...any),
) *refresher {
	ctx, cancel := context.WithCancel(context.Background())
	return &refresher{
		fetch:  fetch,
		save:   save,
		emit:   emit,
		reset:  make(chan struct{}, 1),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start refreshes once and then keeps to the configured interval.
func (r *refresher) Start() {
	r.wg.Add(2)
	go func() {
		defer r.wg.Done()
		r.RefreshAll()
	}()
	go func() {
		defer r.wg.Done()
		r.loop()
	}()
}

// Stop cancels any fetch in flight and waits for the loop to finish.
func (r *refresher) Stop() {
	r.once.Do(r.cancel)
	r.wg.Wait()
}

// Reset restarts the timer, for when the interval changes.
func (r *refresher) Reset() {
	select {
	case r.reset <- struct{}{}:
	default:
	}
}

func (r *refresher) loop() {
	for {
		timer := time.NewTimer(time.Duration(store.GetAutoRefreshMinutes()) * time.Minute)
		select {
		case <-timer.C:
			r.RefreshAll()
		case <-r.reset:
			timer.Stop()
		case <-r.ctx.Done():
			timer.Stop()
			return
		}
	}
}

// RefreshAll updates every subscription server, and skips a pass already under way.
func (r *refresher) RefreshAll() {
	if !r.running.TryLock() {
		return
	}
	defer r.running.Unlock()

	servers, err := store.GetServers()
	if err != nil {
		return
	}
	for _, s := range servers {
		if s.URL == nil || *s.URL == "" {
			continue
		}
		if r.ctx.Err() != nil {
			return
		}
		r.emit("servers:refreshing", serverRefreshEvent{ID: s.ID})
		errMsg := r.RefreshServer(&s.Server)
		r.emit("servers:refresh-done", serverRefreshEvent{ID: s.ID, Error: errMsg})
	}
	r.emit("servers:changed")
}

// RefreshServer replaces a server's nodes, returning "" or a message for the user.
func (r *refresher) RefreshServer(server *store.Server) string {
	ctx, cancel := context.WithTimeout(r.ctx, refreshTimeout)
	defer cancel()

	body, err := r.fetch(ctx, *server.URL)
	if err != nil {
		return err.Error()
	}
	nodes := subs.NodesFromBody(body)
	if subs.IsBlockedPlaceholder(nodes) {
		return "subscription server rejected this client"
	}
	if len(nodes) == 0 {
		return "no nodes found in subscription"
	}
	// Only past the checks above, so a failed fetch leaves the old nodes in place.
	if err := store.DeleteNodesByServer(server.ID); err != nil {
		return err.Error()
	}
	if err := r.save(server, nodes); err != nil {
		return err.Error()
	}
	return ""
}
