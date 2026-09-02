package store

import (
	"path/filepath"
	"testing"
)

// openTestDB points the package at a scratch database; the connection is global.
func openTestDB(t *testing.T) {
	t.Helper()
	if err := InitDBAt(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("InitDBAt: %v", err)
	}
	t.Cleanup(func() { goquDB = nil })
}

// saveServer stores a subscription and returns it with its assigned ID.
func saveServer(t *testing.T, name string) *Server {
	t.Helper()
	s := &Server{Name: name, Kind: KindSubscription}
	if err := s.Save(); err != nil {
		t.Fatalf("Server.Save: %v", err)
	}
	if s.ID == 0 {
		t.Fatal("Save left the server without an ID")
	}
	return s
}

func saveNode(t *testing.T, serverID int64, name, uri string, order int) *Node {
	t.Helper()
	n := &Node{ServerID: serverID, Name: name, URI: uri, Protocol: "vless", SortOrder: order}
	if err := n.Save(); err != nil {
		t.Fatalf("Node.Save: %v", err)
	}
	return n
}

func TestMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for i := 0; i < 3; i++ {
		if err := InitDBAt(path); err != nil {
			t.Fatalf("InitDBAt run %d: %v", i+1, err)
		}
	}
	t.Cleanup(func() { goquDB = nil })

	var applied int
	if err := goquDB.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 2 {
		t.Fatalf("schema_migrations holds %d rows, want one per migration file", applied)
	}
}

func TestServerRoundTrip(t *testing.T) {
	openTestDB(t)

	url := "https://example.com/sub"
	s := &Server{Name: "Netherlands", Kind: KindSubscription, URL: &url}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := GetServerByID(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Netherlands" || got.Kind != KindSubscription {
		t.Errorf("read back %+v, want the saved name and kind", got)
	}
	if got.URL == nil || *got.URL != url {
		t.Errorf("URL = %v, want %q", got.URL, url)
	}
	if got.CreatedAt.IsZero() {
		t.Error("Save left created_at unset")
	}
}

// A manual server has no URL, and the nullable column has to survive that.
func TestServerWithoutURL(t *testing.T) {
	openTestDB(t)

	s := &Server{Name: "pasted", Kind: KindManual}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := GetServerByID(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != nil {
		t.Errorf("URL = %v, want nil", *got.URL)
	}
}

func TestServerSaveUpdatesInPlace(t *testing.T) {
	openTestDB(t)
	s := saveServer(t, "before")

	s.Name = "after"
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	servers, err := GetServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 {
		t.Fatalf("got %d servers, want the update to replace the row", len(servers))
	}
	if servers[0].Name != "after" {
		t.Errorf("name = %q, want the update to stick", servers[0].Name)
	}
}

func TestGetServerByIDMissing(t *testing.T) {
	openTestDB(t)
	if _, err := GetServerByID(404); err == nil {
		t.Fatal("a missing server read back without an error")
	}
}

func TestGetServersOrdersByCreation(t *testing.T) {
	openTestDB(t)
	saveServer(t, "first")
	saveServer(t, "second")

	servers, err := GetServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 || servers[0].Name != "first" || servers[1].Name != "second" {
		t.Fatalf("order = %v, want oldest first", names(servers))
	}
}

func names(servers []ServerWithNodes) []string {
	out := make([]string, len(servers))
	for i, s := range servers {
		out[i] = s.Name
	}
	return out
}

func TestGetServersAttachesNodesInOrder(t *testing.T) {
	openTestDB(t)
	s := saveServer(t, "sub")
	saveNode(t, s.ID, "third", "vless://c@h:443", 2)
	saveNode(t, s.ID, "first", "vless://a@h:443", 0)
	saveNode(t, s.ID, "second", "vless://b@h:443", 1)

	servers, err := GetServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 {
		t.Fatalf("got %d servers, want 1", len(servers))
	}
	got := []string{}
	for _, n := range servers[0].Nodes {
		got = append(got, n.Name)
	}
	want := []string{"first", "second", "third"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("node order = %v, want %v", got, want)
	}
}

// A server with no nodes serializes as an empty list, not null, for the UI.
func TestGetNodesByServerIsNeverNil(t *testing.T) {
	openTestDB(t)
	nodes, err := GetNodesByServer(404)
	if err != nil {
		t.Fatal(err)
	}
	if nodes == nil {
		t.Fatal("GetNodesByServer returned nil")
	}
	if len(nodes) != 0 {
		t.Fatalf("got %d nodes for an unknown server", len(nodes))
	}
}

func TestNodeRoundTrip(t *testing.T) {
	openTestDB(t)
	s := saveServer(t, "sub")
	n := saveNode(t, s.ID, "node", "vless://uuid@example.com:443?type=xhttp", 0)

	got, err := GetNodeByID(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.URI != n.URI || got.ServerID != s.ID || got.Protocol != "vless" {
		t.Errorf("read back %+v, want the saved node", got)
	}
}

func TestGetNodeByURI(t *testing.T) {
	openTestDB(t)
	s := saveServer(t, "sub")
	saveNode(t, s.ID, "wanted", "vless://a@h:443", 0)

	got, err := GetNodeByURI("vless://a@h:443")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Name != "wanted" {
		t.Fatalf("got %v, want the node with that URI", got)
	}

	// An unknown URI is not an error, just no match.
	missing, err := GetNodeByURI("vless://nobody@h:443")
	if err != nil {
		t.Fatalf("unknown URI returned an error: %v", err)
	}
	if missing != nil {
		t.Errorf("got %+v, want nil for an unknown URI", missing)
	}
}

// The same URI can arrive from two subscriptions; lookup has to be deterministic.
func TestGetNodeByURIPrefersTheOlderServer(t *testing.T) {
	openTestDB(t)
	first := saveServer(t, "first")
	second := saveServer(t, "second")
	saveNode(t, second.ID, "from second", "vless://dup@h:443", 0)
	saveNode(t, first.ID, "from first", "vless://dup@h:443", 0)

	got, err := GetNodeByURI("vless://dup@h:443")
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerID != first.ID {
		t.Errorf("matched server %d, want the lower id %d", got.ServerID, first.ID)
	}
}

func TestDeleteServerCascadesToNodes(t *testing.T) {
	openTestDB(t)
	s := saveServer(t, "sub")
	n := saveNode(t, s.ID, "node", "vless://a@h:443", 0)

	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := GetNodeByID(n.ID); err == nil {
		t.Fatal("the node outlived its server: ON DELETE CASCADE is not in effect")
	}
}

func TestDeleteNodesByServerLeavesOtherServersAlone(t *testing.T) {
	openTestDB(t)
	keep := saveServer(t, "keep")
	drop := saveServer(t, "drop")
	saveNode(t, keep.ID, "kept", "vless://a@h:443", 0)
	saveNode(t, drop.ID, "dropped", "vless://b@h:443", 0)

	if err := DeleteNodesByServer(drop.ID); err != nil {
		t.Fatal(err)
	}
	if nodes, err := GetNodesByServer(drop.ID); err != nil || len(nodes) != 0 {
		t.Fatalf("nodes %v err %v, want the server emptied", nodes, err)
	}
	if nodes, err := GetNodesByServer(keep.ID); err != nil || len(nodes) != 1 {
		t.Fatalf("nodes %v err %v, want the other server untouched", nodes, err)
	}
}

func TestNodeDelete(t *testing.T) {
	openTestDB(t)
	s := saveServer(t, "sub")
	n := saveNode(t, s.ID, "node", "vless://a@h:443", 0)

	if err := n.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := GetNodeByID(n.ID); err == nil {
		t.Fatal("the node survived Delete")
	}
}

func TestAutoRefreshMinutes(t *testing.T) {
	openTestDB(t)

	if got := GetAutoRefreshMinutes(); got != DefaultAutoRefreshMinutes {
		t.Errorf("unset interval = %d, want the default %d", got, DefaultAutoRefreshMinutes)
	}
	if err := SetAutoRefreshMinutes(15); err != nil {
		t.Fatal(err)
	}
	if got := GetAutoRefreshMinutes(); got != 15 {
		t.Errorf("interval = %d, want 15", got)
	}
	// Overwriting the same key must upsert rather than fail on the primary key.
	if err := SetAutoRefreshMinutes(45); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if got := GetAutoRefreshMinutes(); got != 45 {
		t.Errorf("interval = %d, want 45", got)
	}
}

func TestAutoRefreshMinutesClamped(t *testing.T) {
	openTestDB(t)
	for _, in := range []int{0, -5} {
		if err := SetAutoRefreshMinutes(in); err != nil {
			t.Fatal(err)
		}
		if got := GetAutoRefreshMinutes(); got != 1 {
			t.Errorf("SetAutoRefreshMinutes(%d) stored %d, want the clamp to 1", in, got)
		}
	}
}

// A value the setting cannot parse falls back rather than propagating a zero.
func TestAutoRefreshMinutesRejectsGarbage(t *testing.T) {
	openTestDB(t)
	if err := setSetting(keyAutoRefreshMinutes, "soon"); err != nil {
		t.Fatal(err)
	}
	if got := GetAutoRefreshMinutes(); got != DefaultAutoRefreshMinutes {
		t.Errorf("garbage produced %d, want the default", got)
	}
}

func TestSelectedNodeURI(t *testing.T) {
	openTestDB(t)

	if got := GetSelectedNodeURI(); got != "" {
		t.Errorf("unset selection = %q, want empty", got)
	}
	if err := SetSelectedNodeURI("vless://a@h:443"); err != nil {
		t.Fatal(err)
	}
	if got := GetSelectedNodeURI(); got != "vless://a@h:443" {
		t.Errorf("selection = %q, want the saved URI", got)
	}
	if err := SetSelectedNodeURI("vless://b@h:443"); err != nil {
		t.Fatal(err)
	}
	if got := GetSelectedNodeURI(); got != "vless://b@h:443" {
		t.Errorf("selection = %q, want the newer URI", got)
	}
}
