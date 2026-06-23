package main

import (
	"context"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ConnState is the (mock, for now) VPN connection state surfaced to the UI.
//
// NOTE: connecting does not yet establish a real tunnel — that lands with the
// sing-box engine milestone. For now Connect/Disconnect only flip this state and
// emit a "vpn:state" event so the UI can be built and verified end to end.
type ConnState struct {
	Connected bool  `json:"connected"`
	NodeID    int64 `json:"nodeId"`
}

type App struct {
	errorWindow *application.WebviewWindow

	mu   sync.Mutex
	conn ConnState
}

func NewApp() *App {
	return &App{}
}

// SetErrorWindow stores the error window reference (called from main before Run).
func (a *App) SetErrorWindow(w *application.WebviewWindow) {
	a.errorWindow = w
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
	return initDB()
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
	parsed, err := parseImport(text)
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
// the connection is dropped.
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

	a.mu.Lock()
	if a.conn.Connected {
		if _, err := GetNodeByID(a.conn.NodeID); err != nil {
			a.conn = ConnState{}
			application.Get().Event.Emit("vpn:state", a.conn)
		}
	}
	a.mu.Unlock()

	application.Get().Event.Emit("servers:changed")
	return true
}

// GetConnState returns the current connection state.
func (a *App) GetConnState() ConnState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn
}

// Connect marks the given node as the active connection (mock, no real tunnel).
func (a *App) Connect(nodeID int64) ConnState {
	if _, err := GetNodeByID(nodeID); err != nil {
		a.showError(err)
		return a.GetConnState()
	}
	a.mu.Lock()
	a.conn = ConnState{Connected: true, NodeID: nodeID}
	state := a.conn
	a.mu.Unlock()

	application.Get().Event.Emit("vpn:state", state)
	return state
}

// Disconnect tears down the active (mock) connection.
func (a *App) Disconnect() ConnState {
	a.mu.Lock()
	a.conn = ConnState{}
	state := a.conn
	a.mu.Unlock()

	application.Get().Event.Emit("vpn:state", state)
	return state
}

// CloseErrorWindow hides the error window.
func (a *App) CloseErrorWindow() {
	if a.errorWindow != nil {
		a.errorWindow.Hide()
	}
}
