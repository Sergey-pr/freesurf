package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"freesurf/internal/engine"
	"freesurf/internal/ping"
	"freesurf/internal/store"
	"freesurf/internal/subs"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// App is the Wails service exposed to the frontend.
type App struct {
	errorWindow *application.WebviewWindow
	logsWindow  *application.WebviewWindow
	engine      *engine.Engine

	refreshReset chan struct{} // ping to restart the auto-refresh timer (interval changed)
	refreshStop  chan struct{} // closed to stop the auto-refresh loop
	refreshMu    sync.Mutex    // guards against concurrent refreshAllSubscriptions runs
}

func NewApp() *App {
	return &App{
		engine:       engine.New(),
		refreshReset: make(chan struct{}, 1),
		refreshStop:  make(chan struct{}),
	}
}

func (a *App) SetErrorWindow(w *application.WebviewWindow) { a.errorWindow = w }
func (a *App) SetLogsWindow(w *application.WebviewWindow)  { a.logsWindow = w }

func (a *App) showError(err error) {
	if err == nil || a.errorWindow == nil {
		return
	}
	application.Get().Event.Emit("app:error", err.Error())
	a.errorWindow.Show()
}

func (a *App) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	engine.ClearSentinel() // always start disconnected
	if err := store.InitDB(); err != nil {
		return err
	}
	go a.refreshAllSubscriptions()
	go a.autoRefreshLoop()
	return nil
}

// autoRefreshLoop periodically refreshes all subscriptions, using the interval
// from settings. It restarts its timer when refreshReset is pinged (after the
// interval changes) and exits when refreshStop is closed.
func (a *App) autoRefreshLoop() {
	for {
		d := time.Duration(store.GetAutoRefreshMinutes()) * time.Minute
		timer := time.NewTimer(d)
		select {
		case <-timer.C:
			a.refreshAllSubscriptions()
		case <-a.refreshReset:
			timer.Stop()
		case <-a.refreshStop:
			timer.Stop()
			return
		}
	}
}

// GetAutoRefreshMinutes returns the subscription auto-refresh interval (minutes).
func (a *App) GetAutoRefreshMinutes() int {
	return store.GetAutoRefreshMinutes()
}

// SetAutoRefreshMinutes persists the auto-refresh interval and restarts the timer.
func (a *App) SetAutoRefreshMinutes(minutes int) int {
	if err := store.SetAutoRefreshMinutes(minutes); err != nil {
		a.showError(err)
		return store.GetAutoRefreshMinutes()
	}
	select {
	case a.refreshReset <- struct{}{}:
	default:
	}
	return store.GetAutoRefreshMinutes()
}

type serverRefreshEvent struct {
	ID    int64  `json:"id"`
	Error string `json:"error,omitempty"`
}

func (a *App) refreshAllSubscriptions() {
	// Skip if a refresh is already running (e.g. startup + timer overlap).
	if !a.refreshMu.TryLock() {
		return
	}
	defer a.refreshMu.Unlock()

	servers, err := store.GetServers()
	if err != nil {
		return
	}
	subs_ := make([]store.ServerWithNodes, 0, len(servers))
	for _, s := range servers {
		if s.URL != nil && *s.URL != "" {
			subs_ = append(subs_, s)
		}
	}
	if len(subs_) == 0 {
		return
	}
	for _, s := range subs_ {
		application.Get().Event.Emit("servers:refreshing", serverRefreshEvent{ID: s.ID})
		errMsg := a.doRefreshServer(&s.Server)
		application.Get().Event.Emit("servers:refresh-done", serverRefreshEvent{ID: s.ID, Error: errMsg})
	}
	application.Get().Event.Emit("servers:changed")
}

// doRefreshServer fetches and saves updated nodes for s. Returns an error string (empty = success).
func (a *App) doRefreshServer(server *store.Server) string {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	body, err := subs.FetchSubscription(ctx, *server.URL)
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
	if err := store.DeleteNodesByServer(server.ID); err != nil {
		return err.Error()
	}
	if _, err := a.saveServer(server, nodes); err != nil {
		return err.Error()
	}
	return ""
}

func (a *App) ServiceShutdown() error {
	close(a.refreshStop)
	a.engine.Shutdown()
	return nil
}

// UninstallHelper removes the privileged helper (one password prompt).
func (a *App) UninstallHelper() bool {
	if err := engine.UninstallHelper(); err != nil {
		a.showError(err)
		return false
	}
	return true
}

func (a *App) HelperInstalled() bool { return engine.HelperInstalled() }

// GetServers returns all servers, each with its nodes, for rendering the list.
func (a *App) GetServers() []store.ServerWithNodes {
	servers, err := store.GetServers()
	if err != nil {
		a.showError(err)
		return []store.ServerWithNodes{}
	}
	return servers
}

// AddFromClipboard imports a server (and nodes) from the system clipboard.
func (a *App) AddFromClipboard() *store.ServerWithNodes {
	text, ok := application.Get().Clipboard.Text()
	if !ok || text == "" {
		a.showError(subs.ErrEmptyImport{})
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	parsed, err := subs.BuildImport(ctx, text)
	if err != nil {
		a.showError(err)
		return nil
	}

	server := &store.Server{Name: parsed.Name, Kind: parsed.Kind, URL: parsed.URL}
	saved, err := a.saveServer(server, parsed.Nodes)
	if err != nil {
		a.showError(err)
		return nil
	}
	application.Get().Event.Emit("servers:changed")
	return saved
}

// RefreshServer re-fetches a subscription server's URL and replaces its nodes.
func (a *App) RefreshServer(id int64) *store.ServerWithNodes {
	server, err := store.GetServerByID(id)
	if err != nil {
		a.showError(err)
		return nil
	}
	if server.URL == nil || *server.URL == "" {
		a.showError(fmt.Errorf("this server has no subscription URL to refresh"))
		return nil
	}

	application.Get().Event.Emit("servers:refreshing", serverRefreshEvent{ID: id})
	errMsg := a.doRefreshServer(server)
	application.Get().Event.Emit("servers:refresh-done", serverRefreshEvent{ID: id, Error: errMsg})
	if errMsg != "" {
		return nil
	}

	nodes, err := store.GetNodesByServer(id)
	if err != nil {
		return nil
	}
	application.Get().Event.Emit("servers:changed")
	return &store.ServerWithNodes{Server: *server, Nodes: nodes}
}

// saveServer inserts the server (if new) and its nodes, returning the combined view.
func (a *App) saveServer(server *store.Server, nodes []store.Node) (*store.ServerWithNodes, error) {
	if err := server.Save(); err != nil {
		return nil, err
	}
	saved := make([]store.Node, 0, len(nodes))
	for i := range nodes {
		n := nodes[i]
		n.ServerID = server.ID
		if err := n.Save(); err != nil {
			return nil, err
		}
		saved = append(saved, n)
	}
	return &store.ServerWithNodes{Server: *server, Nodes: saved}, nil
}

// PingNode returns the TCP connect latency (ms) to a node's server, or -1 on failure.
func (a *App) PingNode(id int64) int {
	node, err := store.GetNodeByID(id)
	if err != nil {
		a.showError(err)
		return -1
	}
	return ping.URI(node.URI)
}

// PingServer pings all nodes of a server concurrently, returning nodeID → ms.
func (a *App) PingServer(id int64) map[int64]int {
	nodes, err := store.GetNodesByServer(id)
	if err != nil {
		a.showError(err)
		return map[int64]int{}
	}
	uris := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		uris[n.ID] = n.URI
	}
	return ping.All(uris)
}

func (a *App) RenameServer(id int64, name string) *store.Server {
	server, err := store.GetServerByID(id)
	if err != nil {
		a.showError(err)
		return nil
	}
	server.Name = name
	if err := server.Save(); err != nil {
		a.showError(err)
		return nil
	}
	application.Get().Event.Emit("servers:changed")
	return server
}

// DeleteServer removes a server and its nodes, dropping the connection if the
// active node belonged to it.
func (a *App) DeleteServer(id int64) bool {
	server, err := store.GetServerByID(id)
	if err != nil {
		a.showError(err)
		return false
	}
	if err := server.Delete(); err != nil {
		a.showError(err)
		return false
	}

	if st := a.engine.State(); st.Status != engine.StatusDisconnected {
		if _, err := store.GetNodeByID(st.NodeID); err != nil {
			a.engine.Disconnect()
		}
	}

	application.Get().Event.Emit("servers:changed")
	return true
}

func (a *App) GetConnState() engine.ConnState { return a.engine.State() }

// Connect brings up the tunnel to the given node.
func (a *App) Connect(nodeID int64) engine.ConnState {
	node, err := store.GetNodeByID(nodeID)
	if err != nil {
		a.showError(err)
		return a.engine.State()
	}
	state, err := a.engine.Connect(node)
	if err != nil {
		a.showError(err)
	}
	return state
}

func (a *App) Disconnect() engine.ConnState { return a.engine.Disconnect() }

func (a *App) CloseErrorWindow() {
	if a.errorWindow != nil {
		a.errorWindow.Hide()
	}
}

func (a *App) GetLog() string { return a.engine.LogText() }
func (a *App) ClearLog()      { a.engine.ClearLog() }

func (a *App) OpenLogsWindow() {
	if a.logsWindow != nil {
		a.logsWindow.Show()
		a.logsWindow.Focus()
	}
}

func (a *App) CloseLogsWindow() {
	if a.logsWindow != nil {
		a.logsWindow.Hide()
	}
}
