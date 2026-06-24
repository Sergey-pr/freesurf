package main

import (
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
)

var nodeTable = goqu.T("nodes")

type Node struct {
	ID        int64     `db:"id"         json:"id"         goqu:"skipinsert,skipupdate"`
	ServerID  int64     `db:"server_id"  json:"serverId"`
	Name      string    `db:"name"       json:"name"`
	URI       string    `db:"uri"        json:"uri"`
	Protocol  string    `db:"protocol"   json:"protocol"`
	SortOrder int       `db:"sort_order" json:"sortOrder"`
	CreatedAt time.Time `db:"created_at" json:"createdAt" goqu:"skipupdate"`
}

// GetNodesByServer returns the nodes of a server ordered by sort_order.
func GetNodesByServer(serverID int64) ([]Node, error) {
	var nodes []Node
	err := goquDB.From(nodeTable).
		Where(goqu.C("server_id").Eq(serverID)).
		Order(goqu.I("sort_order").Asc(), goqu.I("id").Asc()).
		ScanStructs(&nodes)
	if nodes == nil {
		nodes = []Node{}
	}
	return nodes, err
}

// DeleteNodesByServer removes all nodes belonging to a server.
func DeleteNodesByServer(serverID int64) error {
	_, err := goquDB.Delete(nodeTable).
		Where(goqu.C("server_id").Eq(serverID)).
		Executor().Exec()
	return err
}

// GetNodeByID returns the node with the given id, or an error if not found.
func GetNodeByID(id int64) (*Node, error) {
	var n Node
	found, err := goquDB.From(nodeTable).
		Where(goqu.C("id").Eq(id)).
		ScanStruct(&n)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("node %d not found", id)
	}
	return &n, nil
}

// Save inserts or updates the node. ID == 0 means a new record.
func (n *Node) Save() error {
	if n.ID == 0 {
		n.CreatedAt = time.Now()
		result, err := goquDB.Insert(nodeTable).Rows(n).Executor().Exec()
		if err != nil {
			return err
		}
		n.ID, err = result.LastInsertId()
		return err
	}
	_, err := goquDB.Update(nodeTable).
		Set(n).
		Where(goqu.C("id").Eq(n.ID)).
		Executor().Exec()
	return err
}

// Delete removes the node from the database.
func (n *Node) Delete() error {
	_, err := goquDB.Delete(nodeTable).
		Where(goqu.C("id").Eq(n.ID)).
		Executor().Exec()
	return err
}
