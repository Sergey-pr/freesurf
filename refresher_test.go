package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"freesurf/internal/store"
)

// eventLog records what the refresher told the frontend.
type eventLog struct {
	mu   sync.Mutex
	sent []string
	errs map[int64]string
}

func (e *eventLog) emit(name string, data ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sent = append(e.sent, name)
	if len(data) == 0 {
		return
	}
	if ev, ok := data[0].(serverRefreshEvent); ok && name == "servers:refresh-done" {
		if e.errs == nil {
			e.errs = map[int64]string{}
		}
		e.errs[ev.ID] = ev.Error
	}
}

func (e *eventLog) names() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.sent...)
}

func (e *eventLog) errFor(id int64) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.errs[id]
}

func openDB(t *testing.T) {
	t.Helper()
	if err := store.InitDBAt(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("InitDBAt: %v", err)
	}
	t.Cleanup(func() { _ = store.CloseDB() })
}

// saveNodes mirrors the App adapter without needing a Wails application.
func saveNodes(server *store.Server, nodes []store.Node) error {
	if err := server.Save(); err != nil {
		return err
	}
	for i := range nodes {
		n := nodes[i]
		n.ServerID = server.ID
		if err := n.Save(); err != nil {
			return err
		}
	}
	return nil
}

func subscription(t *testing.T, name, url string) *store.Server {
	t.Helper()
	s := &store.Server{Name: name, Kind: store.KindSubscription, URL: &url}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	return s
}

const twoNodes = "vless://a@one.example.com:443?type=tcp\nvless://b@two.example.com:443?type=tcp\n"

func TestRefreshAllReplacesNodes(t *testing.T) {
	openDB(t)
	s := subscription(t, "sub", "https://example.com/sub")
	if err := saveNodes(s, []store.Node{{Name: "stale", URI: "vless://old@h:443"}}); err != nil {
		t.Fatal(err)
	}

	ev := &eventLog{}
	r := newRefresher(
		func(context.Context, string) (string, error) { return twoNodes, nil },
		saveNodes, ev.emit,
	)
	r.RefreshAll()

	nodes, err := store.GetNodesByServer(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want the subscription's two", len(nodes))
	}
	for _, n := range nodes {
		if n.Name == "stale" {
			t.Error("the old node survived the refresh")
		}
	}
	if got := ev.errFor(s.ID); got != "" {
		t.Errorf("refresh reported %q, want success", got)
	}
	want := []string{"servers:refreshing", "servers:refresh-done", "servers:changed"}
	if got := ev.names(); len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("eventLog = %v, want %v", got, want)
	}
}

func TestRefreshAllSkipsServersWithoutAURL(t *testing.T) {
	openDB(t)
	manual := &store.Server{Name: "pasted", Kind: store.KindManual}
	if err := manual.Save(); err != nil {
		t.Fatal(err)
	}
	empty := ""
	blank := &store.Server{Name: "blank", Kind: store.KindSubscription, URL: &empty}
	if err := blank.Save(); err != nil {
		t.Fatal(err)
	}

	ev := &eventLog{}
	var fetched int
	r := newRefresher(
		func(context.Context, string) (string, error) { fetched++; return twoNodes, nil },
		saveNodes, ev.emit,
	)
	r.RefreshAll()

	if fetched != 0 {
		t.Errorf("fetched %d times, want none for servers with no subscription URL", fetched)
	}
}

// A failed refresh must leave the nodes the user already has.
func TestRefreshServerKeepsNodesOnFailure(t *testing.T) {
	openDB(t)
	s := subscription(t, "sub", "https://example.com/sub")
	if err := saveNodes(s, []store.Node{{Name: "keep", URI: "vless://old@h:443"}}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		body    string
		fetch   error
		wantMsg string
	}{
		{name: "fetch fails", fetch: errors.New("connection refused"), wantMsg: "connection refused"},
		{name: "blocked", body: "vless://x@subscription.blocked:443", wantMsg: "rejected this client"},
		{name: "nothing parses", body: "not a share link", wantMsg: "no nodes found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := &eventLog{}
			r := newRefresher(
				func(context.Context, string) (string, error) { return tc.body, tc.fetch },
				saveNodes, ev.emit,
			)
			if got := r.RefreshServer(s); got == "" || !strings.Contains(got, tc.wantMsg) {
				t.Fatalf("message = %q, want it to mention %q", got, tc.wantMsg)
			}
			nodes, err := store.GetNodesByServer(s.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(nodes) != 1 || nodes[0].Name != "keep" {
				t.Errorf("nodes = %v, want the existing one untouched", nodes)
			}
		})
	}
}

// Startup and the timer can overlap, and a second pass must not double the work.
func TestRefreshAllRunsOneAtATime(t *testing.T) {
	openDB(t)
	subscription(t, "sub", "https://example.com/sub")

	entered := make(chan struct{})
	release := make(chan struct{})
	var fetched int
	var mu sync.Mutex
	ev := &eventLog{}
	r := newRefresher(
		func(context.Context, string) (string, error) {
			mu.Lock()
			fetched++
			mu.Unlock()
			close(entered)
			<-release
			return twoNodes, nil
		},
		saveNodes, ev.emit,
	)

	done := make(chan struct{})
	go func() { r.RefreshAll(); close(done) }()
	<-entered

	r.RefreshAll() // must return immediately rather than queue behind the first
	close(release)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if fetched != 1 {
		t.Errorf("fetched %d times, want the second pass skipped", fetched)
	}
}

// Stop has to cut a fetch short, otherwise shutdown waits out the timeout.
func TestStopCancelsAFetchInFlight(t *testing.T) {
	openDB(t)
	subscription(t, "sub", "https://example.com/sub")

	entered := make(chan struct{})
	ev := &eventLog{}
	r := newRefresher(
		func(ctx context.Context, _ string) (string, error) {
			close(entered)
			<-ctx.Done()
			return "", ctx.Err()
		},
		saveNodes, ev.emit,
	)

	r.Start()
	<-entered

	stopped := make(chan struct{})
	go func() { r.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return: the fetch was left running")
	}
}

func TestStopIsIdempotent(t *testing.T) {
	openDB(t)
	r := newRefresher(
		func(context.Context, string) (string, error) { return twoNodes, nil },
		saveNodes, func(string, ...any) {},
	)
	r.Start()
	r.Stop()
	r.Stop()
}

// Reset must restart the timer without wedging the loop.
func TestResetKeepsTheLoopAlive(t *testing.T) {
	openDB(t)
	r := newRefresher(
		func(context.Context, string) (string, error) { return twoNodes, nil },
		saveNodes, func(string, ...any) {},
	)
	r.Start()
	for i := 0; i < 5; i++ {
		r.Reset()
	}

	stopped := make(chan struct{})
	go func() { r.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop stopped responding after Reset")
	}
}
