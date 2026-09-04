package main

import (
	"context"
	"errors"
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

	refresher *refresher
}

func NewApp() *App {
	a := &App{engine: engine.New()}
	a.refresher = newRefresher(subs.FetchSubscription, a.saveNodes, emitEvent)
	return a
}

// emitEvent wraps the Wails bus so the refresher never touches the singleton.
func emitEvent(name string, data ...any) { application.Get().Event.Emit(name, data...) }

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
	a.refresher.Start()
	return nil
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
	a.refresher.Reset()
	return store.GetAutoRefreshMinutes()
}

// GetSelectedNodeID returns the last selected node, resolved by URI so the choice
// survives a subscription refresh that renumbers nodes. 0 means none.
func (a *App) GetSelectedNodeID() int64 {
	uri := store.GetSelectedNodeURI()
	if uri == "" {
		return 0
	}
	node, err := store.GetNodeByURI(uri)
	if err != nil || node == nil {
		return 0
	}
	return node.ID
}

// SetSelectedNodeID persists the selected node; 0 clears the selection.
func (a *App) SetSelectedNodeID(id int64) {
	uri := ""
	if id != 0 {
		node, err := store.GetNodeByID(id)
		if err != nil {
			return
		}
		uri = node.URI
	}
	if err := store.SetSelectedNodeURI(uri); err != nil {
		a.showError(err)
	}
}

type serverRefreshEvent struct {
	ID    int64  `json:"id"`
	Error string `json:"error,omitempty"`
}

// pingResultEvent is pushed to the frontend as each node's probe completes, so the
// UI can show and re-sort latencies live instead of waiting for the whole batch.
type pingResultEvent struct {
	NodeID int64 `json:"nodeId"`
	MS     int   `json:"ms"`
}

func (a *App) ServiceShutdown() error {
	a.refresher.Stop()
	a.engine.Shutdown()
	// Safe only after the refresher has stopped querying it.
	return store.CloseDB()
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

// ReinstallDependencies force-reinstalls the embedded core binaries (sing-box,
// Xray, and the Wintun driver on Windows). Fails while the tunnel is up.
func (a *App) ReinstallDependencies() bool {
	if err := a.engine.ReinstallCores(); err != nil {
		a.showError(err)
		return false
	}
	return true
}

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
	errMsg := a.refresher.RefreshServer(server)
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
// saveNodes stores a server and its nodes, discarding the assembled result.
func (a *App) saveNodes(server *store.Server, nodes []store.Node) error {
	_, err := a.saveServer(server, nodes)
	return err
}

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

// PingNode returns the connect latency (ms) to a node's server, or -1 on failure,
// logging the probe outcome (and failure reason) to the log window.
func (a *App) PingNode(id int64) int {
	node, err := store.GetNodeByID(id)
	if err != nil {
		a.showError(err)
		return -1
	}
	r := ping.Probe(node.URI)
	a.engine.Logf("ping %s: %s", node.Name, r.Log())
	application.Get().Event.Emit("ping:result", pingResultEvent{NodeID: id, MS: r.MS})
	return r.MS
}

// PingServer pings all nodes of a server concurrently, returning nodeID → ms and
// logging each probe outcome (and failure reason) to the log window.
func (a *App) PingServer(id int64) map[int64]int {
	nodes, err := store.GetNodesByServer(id)
	if err != nil {
		a.showError(err)
		return map[int64]int{}
	}
	uris := make(map[int64]string, len(nodes))
	names := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		uris[n.ID] = n.URI
		names[n.ID] = n.Name
	}
	out := make(map[int64]int, len(nodes))
	var mu sync.Mutex
	// Emit each result the moment its probe finishes so the UI updates and re-sorts
	// live, rather than waiting for the slowest node in the batch.
	ping.AllDetailedFunc(uris, func(nid int64, r ping.Result) {
		a.engine.Logf("ping %s: %s", names[nid], r.Log())
		application.Get().Event.Emit("ping:result", pingResultEvent{NodeID: nid, MS: r.MS})
		mu.Lock()
		out[nid] = r.MS
		mu.Unlock()
	})
	return out
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
	// A double-click or a Stop mid-connect is not worth a dialog.
	if err != nil && !errors.Is(err, engine.ErrBusy) && !errors.Is(err, engine.ErrCancelled) {
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
		a.setLogStreaming(true)
	}
}

func (a *App) CloseLogsWindow() {
	if a.logsWindow != nil {
		a.logsWindow.Hide()
		a.setLogStreaming(false)
	}
}

// setLogStreaming follows the logs window's visibility and reloads it on reopen.
func (a *App) setLogStreaming(on bool) {
	a.engine.SetLogStreaming(on)
	if on {
		application.Get().Event.Emit("log:reload")
	}
}
