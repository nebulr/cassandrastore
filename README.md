cassandrastore
==========

Gorilla's Session Store Implementation for Cassandra

Installation
===========

Run `go get github.com/nebulr/cassandrastore` from command line. Gets installed in `$GOPATH`

Usage
=====

`NewCassandraStore` takes the following paramaters

    hosts - an array of ip address strings
    keyspace - keyspace where <tableName> store is created
    tableName - table where sessions are to be saved. Required fields are created automatically if the table doesnot exist.
    path - path for Set-Cookie header
    maxAge 
    codecs

Internally, `cassadrastore` uses [this](https://github.com/gocql/gocql) Cassandra driver.

e.g.,
      

      package main
  
      import (
  	    "fmt"
  	    "github.com/nebulr/cassandrastore"
  	    "net/http"
      )
  
      var store *cassandrastore.CassandraStore
  
      func sessTest(w http.ResponseWriter, r *http.Request) {
  	    session, err := store.Get(r, "foobar")
  	    session.Values["bar"] = "baz"
  	    session.Values["baz"] = "foo"
  	    err = session.Save(r, w)
  	    fmt.Printf("%#v\n", session)
  	    fmt.Println(err)
      }

    func main() {
        store, err := cassandrastore.CassandraStore([1]string{"127.0.0.1"}, <keyspace>, <tablename>, "/", 3600, []byte("<SecretKey>"))
        if err != nil {
          panic(err)
        }
        defer store.Close()

    	http.HandleFunc("/", sessTest)
    	http.ListenAndServe(":8080", nil)
    }

Credit to @srinathgs for his initial work on [this MysqlStore](https://github.com/srinathgs/mysqlstore)