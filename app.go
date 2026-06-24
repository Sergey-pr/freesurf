package main

import (
	"context"
	"fmt"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type App struct {
	errorWindow *application.WebviewWindow
	logsWindow  *application.WebviewWindow
	engine      *Engine
}

func NewApp() *App {
	return &App{engine: newEngine()}
}

// SetErrorWindow stores the error window reference (called from main before Run).
func (a *App) SetErrorWindow(w *application.WebviewWindow) {
	a.errorWindow = w
}

// SetLogsWindow stores the logs window reference (called from main before Run).
func (a *App) SetLogsWindow(w *application.WebviewWindow) {
	a.logsWindow = w
}

func (a *App) showError(err error) {
	if err == nil || a.errorWindow == nil {
		return
	}
	application.Get().Event.Emit("app:error", err.Error())
	a.errorWindow.Show()
}

// ServiceStartup is called by the Wails v3 service system when the app starts.
func (a *App) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	// Drop any stale sentinel so we always start disconnected (and a left-over
	// sentinel from a previous crash doesn't keep launchd running the tunnel).
	stopTunnel()
	return initDB()
}

// ServiceShutdown tears down the tunnel on app exit. It must return quickly —
// stopping only removes the sentinel file, so no privilege prompt can block exit.
func (a *App) ServiceShutdown() error {
	a.engine.shutdown()
	return nil
}

// UninstallHelper removes the privileged macOS LaunchDaemon (one password prompt).
func (a *App) UninstallHelper() bool {
	if err := uninstallHelper(); err != nil {
		a.showError(err)
		return false
	}
	return true
}

// HelperInstalled reports whether the privileged helper is installed.
func (a *App) HelperInstalled() bool {
	return helperInstalled()
}

// GetServers returns all servers, each with its nodes, for rendering the list.
func (a *App) GetServers() []ServerWithNodes {
	servers, err := GetServers()
	if err != nil {
		a.showError(err)
		return []ServerWithNodes{}
	}
	return servers
}

// AddFromClipboard reads the system clipboard, parses it into a server (plus
// nodes), persists it and returns the created server. Returns nil on failure.
func (a *App) AddFromClipboard() *ServerWithNodes {
	text, ok := application.Get().Clipboard.Text()
	if !ok || text == "" {
		a.showError(ErrEmptyImport{})
		return nil
	}
	return a.addFromText(text)
}

func (a *App) addFromText(text string) *ServerWithNodes {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	parsed, err := buildImport(ctx, text)
	if err != nil {
		a.showError(err)
		return nil
	}

	server := &Server{Name: parsed.Name, Kind: parsed.Kind, URL: parsed.URL}
	if err := server.Save(); err != nil {
		a.showError(err)
		return nil
	}

	saved := make([]Node, 0, len(parsed.Nodes))
	for i := range parsed.Nodes {
		n := parsed.Nodes[i]
		n.ServerID = server.ID
		if err := n.Save(); err != nil {
			a.showError(err)
			return nil
		}
		saved = append(saved, n)
	}

	result := &ServerWithNodes{Server: *server, Nodes: saved}
	application.Get().Event.Emit("servers:changed")
	return result
}

// RefreshServer re-fetches a subscription server's URL and replaces its nodes.
func (a *App) RefreshServer(id int64) *ServerWithNodes {
	server, err := GetServerByID(id)
	if err != nil {
		a.showError(err)
		return nil
	}
	if server.URL == nil || *server.URL == "" {
		a.showError(fmt.Errorf("this server has no subscription URL to refresh"))
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	body, err := fetchSubscription(ctx, *server.URL)
	if err != nil {
		a.showError(err)
		return nil
	}
	nodes := nodesFromBody(body)
	if isBlockedPlaceholder(nodes) {
		a.showError(fmt.Errorf("the subscription server rejected this client (\"app not supported\")"))
		return nil
	}
	if len(nodes) == 0 {
		a.showError(fmt.Errorf("no servers found in the subscription"))
		return nil
	}

	if err := DeleteNodesByServer(id); err != nil {
		a.showError(err)
		return nil
	}
	saved := make([]Node, 0, len(nodes))
	for i := range nodes {
		n := nodes[i]
		n.ServerID = id
		if err := n.Save(); err != nil {
			a.showError(err)
			return nil
		}
		saved = append(saved, n)
	}

	application.Get().Event.Emit("servers:changed")
	return &ServerWithNodes{Server: *server, Nodes: saved}
}

// PingNode returns the TCP connect latency (ms) to a node's server, or -1 on failure.
func (a *App) PingNode(id int64) int {
	node, err := GetNodeByID(id)
	if err != nil {
		a.showError(err)
		return -1
	}
	return pingNodeURI(node.URI)
}

// PingServer pings all nodes of a server concurrently, returning nodeID → ms (-1 = fail).
func (a *App) PingServer(id int64) map[int64]int {
	nodes, err := GetNodesByServer(id)
	if err != nil {
		a.showError(err)
		return map[int64]int{}
	}
	return pingNodes(nodes)
}

// RenameServer updates a server's display name.
func (a *App) RenameServer(id int64, name string) *Server {
	server, err := GetServerByID(id)
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

// DeleteServer removes a server and its nodes. If the active node belonged to it,
// the tunnel is torn down.
func (a *App) DeleteServer(id int64) bool {
	server, err := GetServerByID(id)
	if err != nil {
		a.showError(err)
		return false
	}
	if err := server.Delete(); err != nil {
		a.showError(err)
		return false
	}

	// If the connected node no longer exists, drop the connection.
	if st := a.engine.state(); st.Status != StatusDisconnected {
		if _, err := GetNodeByID(st.NodeID); err != nil {
			a.engine.disconnect()
		}
	}

	application.Get().Event.Emit("servers:changed")
	return true
}

// GetConnState returns the current connection state.
func (a *App) GetConnState() ConnState {
	return a.engine.state()
}

// Connect brings up the tunnel to the given node.
func (a *App) Connect(nodeID int64) ConnState {
	node, err := GetNodeByID(nodeID)
	if err != nil {
		a.showError(err)
		return a.engine.state()
	}
	state, err := a.engine.connect(node)
	if err != nil {
		a.showError(err)
	}
	return state
}

// Disconnect tears down the active tunnel.
func (a *App) Disconnect() ConnState {
	return a.engine.disconnect()
}

// CloseErrorWindow hides the error window.
func (a *App) CloseErrorWindow() {
	if a.errorWindow != nil {
		a.errorWindow.Hide()
	}
}

// GetLog returns the current engine log buffer as a single string.
func (a *App) GetLog() string {
	return a.engine.logText()
}

// ClearLog empties the engine log buffer.
func (a *App) ClearLog() {
	a.engine.clearLog()
}

// OpenLogsWindow shows (and focuses) the logs window.
func (a *App) OpenLogsWindow() {
	if a.logsWindow != nil {
		a.logsWindow.Show()
		a.logsWindow.Focus()
	}
}

// CloseLogsWindow hides the logs window.
func (a *App) CloseLogsWindow() {
	if a.logsWindow != nil {
		a.logsWindow.Hide()
	}
}
