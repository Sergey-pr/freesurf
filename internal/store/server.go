package store

import (
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
)

// Server kinds.
const (
	KindSubscription = "subscription" // a subscription URL or a multi-node paste
	KindManual       = "manual"       // a single pasted share URI
)

var serverTable = goqu.T("servers")

type Server struct {
	ID        int64     `db:"id"         json:"id"         goqu:"skipinsert,skipupdate"`
	Name      string    `db:"name"       json:"name"`
	Kind      string    `db:"kind"       json:"kind"`
	URL       *string   `db:"url"        json:"url,omitempty"`
	CreatedAt time.Time `db:"created_at" json:"createdAt" goqu:"skipupdate"`
}

// ServerWithNodes is a server together with its child nodes, used by the UI.
type ServerWithNodes struct {
	Server
	Nodes []Node `json:"nodes"`
}

// GetServers returns all servers (oldest first), each with its nodes attached.
func GetServers() ([]ServerWithNodes, error) {
	var servers []Server
	if err := goquDB.From(serverTable).
		Order(goqu.I("created_at").Asc()).
		ScanStructs(&servers); err != nil {
		return nil, err
	}

	result := make([]ServerWithNodes, 0, len(servers))
	for _, s := range servers {
		nodes, err := GetNodesByServer(s.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, ServerWithNodes{Server: s, Nodes: nodes})
	}
	return result, nil
}

func GetServerByID(id int64) (*Server, error) {
	var s Server
	found, err := goquDB.From(serverTable).Where(goqu.C("id").Eq(id)).ScanStruct(&s)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("server %d not found", id)
	}
	return &s, nil
}

// Save inserts or updates the server. ID == 0 means a new record.
func (s *Server) Save() error {
	if s.ID == 0 {
		s.CreatedAt = time.Now()
		result, err := goquDB.Insert(serverTable).Rows(s).Executor().Exec()
		if err != nil {
			return err
		}
		s.ID, err = result.LastInsertId()
		return err
	}
	_, err := goquDB.Update(serverTable).Set(s).Where(goqu.C("id").Eq(s.ID)).Executor().Exec()
	return err
}

// Delete removes the server. Child nodes are removed via ON DELETE CASCADE.
func (s *Server) Delete() error {
	_, err := goquDB.Delete(serverTable).Where(goqu.C("id").Eq(s.ID)).Executor().Exec()
	return err
}
