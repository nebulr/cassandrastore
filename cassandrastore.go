/* Gorilla Sessions backend for Cassandra.

Copyright (c) 2018 Contributors. See the list of contributors in the CONTRIBUTORS file for details.

This software is licensed under a MIT style license available in the LICENSE file.
*/
package cassandrastore

import (
	"github.com/gocql/gocql"
	"encoding/gob"
	"errors"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"
	"log"
	"net/http"
	"strings"
	"time"
)

type CassandraStore struct {
	db         *gocql.Session
	stmtInsert string
	stmtDelete string
	stmtUpdate string
	stmtSelect string

	Codecs  []securecookie.Codec
	Options *sessions.Options
	table   string
}

type sessionRow struct {
	id         string
	data       string
	createdOn  time.Time
	modifiedOn time.Time
	expiresOn  time.Time
}

func init() {
	gob.Register(time.Time{})
}

func NewCassandraStore(hosts []string, keyspace string, tableName string, path string, maxAge int, keyPairs ...[]byte) (*CassandraStore, error) {
	cluster := gocql.NewCluster(hosts...)
	cluster.Keyspace = keyspace
	cluster.Consistency = gocql.Quorum
	db, err := cluster.CreateSession()
	if err != nil {
		return nil, err
	}

	return NewCassandraStoreFromConnection(db, tableName, path, maxAge, keyPairs...)
}

func NewCassandraStoreFromConnection(db *gocql.Session, tableName string, path string, maxAge int, keyPairs ...[]byte) (*CassandraStore, error) {
	// Make sure table name is enclosed.
	tableName = strings.Trim(tableName, "`")

	cTableQ := "CREATE TABLE IF NOT EXISTS " +
		tableName + " (id uuid, " +
		"session_data blob, " +
		"created_on timestamp, " +
		"modified_on timestamp, " +
		"expires_on timestamp, PRIMARY KEY(id))"
	
	if err := db.Query(cTableQ).Exec(); err != nil {
		return nil, err
	}

	stmtInsert := "INSERT INTO " + tableName +
		"(id, session_data, created_on, modified_on, expires_on) VALUES (?, ?, ?, ?, ?)"

	stmtDelete := "DELETE FROM " + tableName + " WHERE id = ?"

	stmtUpdate := "UPDATE " + tableName + " SET session_data = ?, created_on = ?, expires_on = ?, modified_on = ? " +
		"WHERE id = ?"

	stmtSelect := "SELECT id, session_data, created_on, modified_on, expires_on from " +
		tableName + " WHERE id = ?"

	return &CassandraStore{
		db:         db,
		stmtInsert: stmtInsert,
		stmtDelete: stmtDelete,
		stmtUpdate: stmtUpdate,
		stmtSelect: stmtSelect,
		Codecs:     securecookie.CodecsFromPairs(keyPairs...),
		Options: &sessions.Options{
			Path:   path,
			MaxAge: maxAge,
		},
		table: tableName,
	}, nil
}

func (m *CassandraStore) Close() {
	m.db.Close()
}

func (m *CassandraStore) Get(r *http.Request, name string) (*sessions.Session, error) {
	return sessions.GetRegistry(r).Get(m, name)
}

func (m *CassandraStore) New(r *http.Request, name string) (*sessions.Session, error) {
	session := sessions.NewSession(m, name)
	session.Options = &sessions.Options{
		Path:     m.Options.Path,
		Domain:   m.Options.Domain,
		MaxAge:   m.Options.MaxAge,
		Secure:   m.Options.Secure,
		HttpOnly: m.Options.HttpOnly,
	}
	session.IsNew = true
	var err error
	if cook, errCookie := r.Cookie(name); errCookie == nil {
		err = securecookie.DecodeMulti(name, cook.Value, &session.ID, m.Codecs...)
		if err == nil {
			err = m.load(session)
			if err == nil {
				session.IsNew = false
			} else {
				err = nil
			}
		}
	}
	return session, err
}

func (m *CassandraStore) Save(r *http.Request, w http.ResponseWriter, session *sessions.Session) error {
	var err error
	if session.ID == "" {
		if err = m.insert(session); err != nil {
			return err
		}
	} else if err = m.save(session); err != nil {
		return err
	}
	encoded, err := securecookie.EncodeMulti(session.Name(), session.ID, m.Codecs...)
	if err != nil {
		return err
	}
	http.SetCookie(w, sessions.NewCookie(session.Name(), encoded, session.Options))
	return nil
}

func (m *CassandraStore) insert(session *sessions.Session) error {
	var createdOn time.Time
	var modifiedOn time.Time
	var expiresOn time.Time
	var id gocql.UUID;

	crOn := session.Values["created_on"]
	if crOn == nil {
		createdOn = time.Now()
	} else {
		createdOn = crOn.(time.Time)
	}
	modifiedOn = createdOn
	exOn := session.Values["expires_on"]
	if exOn == nil {
		expiresOn = time.Now().Add(time.Second * time.Duration(session.Options.MaxAge))
	} else {
		expiresOn = exOn.(time.Time)
	}
	delete(session.Values, "created_on")
	delete(session.Values, "expires_on")
	delete(session.Values, "modified_on")

	encoded, encErr := securecookie.EncodeMulti(session.Name(), session.Values, m.Codecs...)
	if encErr != nil {
		return encErr
	}

	id, _ = gocql.RandomUUID();

	insErr := m.db.Query(m.stmtInsert, id, encoded, createdOn, modifiedOn, expiresOn).Exec()

	if insErr != nil {
		return insErr
	}
	
	session.ID = id.String()

	return nil
}

func (m *CassandraStore) Delete(r *http.Request, w http.ResponseWriter, session *sessions.Session) error {

	// Set cookie to expire.
	options := *session.Options
	options.MaxAge = -1
	http.SetCookie(w, sessions.NewCookie(session.Name(), "", &options))
	// Clear session values.
	for k := range session.Values {
		delete(session.Values, k)
	}

	delErr := m.db.Query(m.stmtDelete, session.ID).Exec()
	if delErr != nil {
		return delErr
	}
	return nil
}

func (m *CassandraStore) save(session *sessions.Session) error {
	if session.IsNew == true {
		return m.insert(session)
	}
	var createdOn time.Time
	var expiresOn time.Time
	var modifiedOn time.Time
	crOn := session.Values["created_on"]
	if crOn == nil {
		createdOn = time.Now()
	} else {
		createdOn = crOn.(time.Time)
	}
	modifiedOn = createdOn
	exOn := session.Values["expires_on"]
	if exOn == nil {
		expiresOn = time.Now().Add(time.Second * time.Duration(session.Options.MaxAge))
		log.Print("nil")
	} else {
		expiresOn = exOn.(time.Time)
		if expiresOn.Sub(time.Now().Add(time.Second*time.Duration(session.Options.MaxAge))) < 0 {
			expiresOn = time.Now().Add(time.Second * time.Duration(session.Options.MaxAge))
		}
	}

	delete(session.Values, "created_on")
	delete(session.Values, "expires_on")
	delete(session.Values, "modified_on")
	encoded, encErr := securecookie.EncodeMulti(session.Name(), session.Values, m.Codecs...)
	if encErr != nil {
		return encErr
	}
	updErr := m.db.Query(m.stmtUpdate, encoded, createdOn, expiresOn, modifiedOn, session.ID).Exec()
	if updErr != nil {
		return updErr
	}
	return nil
}

func (m *CassandraStore) load(session *sessions.Session) error {
	sess := sessionRow{}
	scanErr := m.db.Query(m.stmtSelect, session.ID).Scan(&sess.id, &sess.data, &sess.createdOn, &sess.modifiedOn, &sess.expiresOn)
	if scanErr != nil {
		return scanErr
	}
	if sess.expiresOn.Sub(time.Now()) < 0 {
		log.Printf("Session expired on %s, but it is %s now.", sess.expiresOn, time.Now())
		return errors.New("Session expired")
	}
	err := securecookie.DecodeMulti(session.Name(), sess.data, &session.Values, m.Codecs...)
	if err != nil {
		return err
	}
	session.Values["created_on"] = sess.createdOn
	session.Values["modified_on"] = sess.modifiedOn
	session.Values["expires_on"] = sess.expiresOn
	return nil

}
