package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strings"

	"github.com/goldic/js"
	"github.com/metadata-driven/mdd/driver"
)

// Connection provides access to a MySQL database.
type Connection struct {
	scheme *driver.Database
	op     *url.URL
	db     *sql.DB
}

var _ = driver.Register(newConnection, "mysql")

// SQLTracer receives SQL statements when non-nil.
var SQLTracer *log.Logger

func newConnection(scheme *driver.Database) (driver.Connection, error) {
	c := &Connection{
		scheme: scheme,
		op:     scheme.ConnectionURL(),
	}
	var err error
	c.db, err = c.openDBConnection()
	if err != nil {
		return nil, err
	}
	if err = c.Rebase(); err != nil {
		c.db.Close()
		return nil, err
	}
	return c, nil
}

// String returns the connection string.
func (c *Connection) String() string {
	return c.scheme.Connection
}

// Params returns the parsed connection URL.
func (c *Connection) Params() *url.URL {
	return c.op
}

// Close closes the database connection pool.
func (c *Connection) Close() error {
	return c.db.Close()
}

// OpenTransaction starts a MySQL transaction controlled by ctx.
func (c *Connection) OpenTransaction(ctx context.Context) (driver.Transaction, error) {
	return newTransaction(c, ctx)
}

// Rebase updates the database schema to match the configured schema.
func (c *Connection) Rebase() (err error) {
	//println("------- rebase for ", c.Params().String())
	defer recoverError(&err)

	for _, tab := range c.scheme.Tables {
		for _, idx := range tab.Indexes {
			for _, name := range idx.Columns {
				col := tab.ColumnByName(name)
				if col != nil && col.Type == driver.TypeString && dbColumnType(col) == "longtext" {
					return fmt.Errorf("mysql: index %s cannot index longtext column %s.%s", idx.Name, tab.Name, name)
				}
			}
		}
	}

	tx := noErrVal(newTransaction(c, context.Background()))
	defer tx.Rollback()
	dbTabs := tx.Tables()
	for _, tab := range c.scheme.Tables {
		//println("- REBASE TABLE ", tab.Name)
		if !slices.Contains(dbTabs, tab.Name) {
			tx.CreateTable(tab.Name)
		}
		dbCols := map[string]*dbColumnInfo{}
		for _, col := range tx.TableColumns(tab.Name) {
			dbCols[col.Name] = col
		}
		for _, col := range tab.Columns {
			if col.Name == "" {
				log.Printf("mysql> WARNING: empty column name for %s (%s)", tab.Name, js.IndentEncode(col))
				continue
			}
			if dbCol := dbCols[col.Name]; dbCol == nil {
				tx.AddTableColumn(tab.Name, col)
			} else {
				if col.Type == driver.TypeJSON && dbCol.Type == "json" {
					tx.exec(fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s LONGTEXT NULL", escName(tab.Name), escName(col.Name)))
					dbCol.Type = "longtext"
					dbCol.Nullable = true
				}
				if dbCol.Nullable || col.Type == driver.TypeJSON {
					tx.normalizeNullColumn(tab.Name, col, dbCol.Nullable)
				}
				if dbCol.Type != dbColumnType(col) || dbCol.Nullable {
					tx.AlterTableColumn(tab.Name, col, "")
				}
			}
			delete(dbCols, col.Name)
		}
		//-- drop columns --
		delete(dbCols, "id")
		// todo: ?? delete by special comment ??
		for name := range dbCols {
			if !strings.HasPrefix(name, "_") { // extended columns
				tx.DropTableColumn(tab.Name, name)
			}
		}

		//---- add indexes
		dbIdxs := map[string]*dbIndexInfo{}
		for _, idx := range tx.TableIndexes(tab.Name) {
			dbIdxs[idx.Name] = idx
		}
		for _, idx := range tab.Indexes {
			idxType := idx.Type
			if idxType == "" {
				idxType = driver.IndexTypeIndex
			}
			if dbIdx := dbIdxs[idx.Name]; dbIdx == nil {
				tx.AddTableIndex(tab.Name, idx)
			} else if !dbIdx.Managed || dbIdx.Type != idxType || strings.Join(dbIdx.Columns, ",") != strings.Join(idx.Columns, ",") {
				tx.DropTableIndex(tab.Name, idx.Name)
				tx.AddTableIndex(tab.Name, idx)
			}
			delete(dbIdxs, idx.Name)
		}
		// delete old indexes with prefix idx_
		for _, idx := range dbIdxs {
			if idx.Managed || strings.HasPrefix(idx.Name, "idx_") {
				tx.DropTableIndex(tab.Name, idx.Name)
			}
		}
	}
	noErr(tx.Commit())
	return
}

func noErr(err error) {
	if err != nil {
		panic(err)
	}
}

func noErrVal[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func recoverError(err *error) {
	if r := recover(); r != nil {
		if err != nil {
			*err = fmt.Errorf("%v", r)
		}
	}
}
