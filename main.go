package main

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/garyburd/redigo/redis"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gocql/gocql"
	_ "github.com/lib/pq"
	"github.com/streadway/amqp"
	"gopkg.in/mgo.v2"
)

var indexTemplate = `
<!DOCTYPE html>
<html>
	<head>
		<link rel="stylesheet" href="//maxcdn.bootstrapcdn.com/bootstrap/3.3.7/css/bootstrap.min.css" integrity="sha256-916EbMg70RQy9LHiGkXzG8hSg9EdNy97GazNG/aiY1w=" crossorigin="anonymous">
		<title>DEMO App</title>
	</head>
	<body>
		<nav class="navbar navbar-default">
			<div class="container-fluid">
				<div class="navbar-header">
					<span class="navbar-brand">CF Monitoring Demo Application</span>
				</div>
			</div>
		</nav>

		<div class="container" >
			<a class="btn btn-primary {{ if index . "mysql" }}disabled{{ end }}" href="/mysql">MySQL</a>
			<a class="btn btn-success {{ if index . "pgsql" }}disabled{{ end }}" href="/pgsql">PostgreSQL</a>
			<a class="btn btn-danger {{ if index . "redis" }}disabled{{ end }}" href="/redis">Redis</a>
			<a class="btn btn-info {{ if index . "memcache" }}disabled{{ end }}" href="/memcache">Memcache</a>
			<a class="btn btn-warning {{ if index . "mongodb" }}disabled{{ end }}" href="/mongodb">MongoDB</a>
			<a class="btn btn-default {{ if index . "cassandra" }}disabled{{ end }}" href="/cassandra">Cassandra</a>
			<a class="btn btn-default {{ if index . "rabbitmq" }}disabled{{ end }}" href="/rabbitmq">RabbitMQ</a>
		</div>

		<script src="//code.jquery.com/jquery-3.1.1.min.js" integrity="sha256-hVVnYaiADRTO2PzUGmuLJr8BLUSjGIZsDYGmIJLv2b8=" crossorigin="anonymous"></script>
		<script src="//maxcdn.bootstrapcdn.com/bootstrap/3.3.7/js/bootstrap.min.js" integrity="sha256-U5ZEeKfGNOja007MMD3YBI0A3OSZOQbeG6z2f2Y0hu8=" crossorigin="anonymous"></script>
	</body>
</html>
`

var mu = sync.Mutex{}
var ss = map[string]bool{}

func main() {
	tpl, err := template.New("index").Parse(indexTemplate)
	if err != nil {
		panic(err)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		w.WriteHeader(http.StatusOK)
		if err := tpl.ExecuteTemplate(w, "index", ss); err != nil {
			panic(err)
		}
	})

	sec := envInt("LOAD_SEC", 900)

	for path, s := range map[string]*struct {
		fn   func(string, func() bool) error
		addr string
	}{
		"mysql":     {testMySQL, envStringMust("MYSQL_URL")},
		"pgsql":     {testPGSQL, envStringMust("PGSQL_URL")},
		"redis":     {testRedis, envStringMust("REDIS_URL")},
		"memcache":  {testMemcache, envStringMust("MEMCACHE_ADDR")},
		"mongodb":   {testMongoDB, envStringMust("MONGODB_URL")},
		"cassandra": {testCassandra, envStringMust("CASSANDRA_URL")},
		"rabbitmq":  {testRabbitMQ, envStringMust("RABBITMQ_URL")},
	} {
		s := s
		path := path

		http.HandleFunc("/"+path, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			if ss[path] {
				http.Error(w, fmt.Sprintf("%s is busy now", path), http.StatusInternalServerError)
				mu.Unlock()
				return
			}
			ss[path] = true
			mu.Unlock()

			go func() {
				defer func() {
					mu.Lock()
					delete(ss, path)
					mu.Unlock()
				}()

				if err := s.fn(s.addr, makeTimer(sec)); err != nil {
					fmt.Fprintf(os.Stderr, "%s error: %v\n", path, err)
				}
				fmt.Printf("%s %dsec done\n", path, sec)
			}()

			http.Redirect(w, r, "/", http.StatusFound)
		})
	}

	// cf compatibility
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	panic(http.ListenAndServe(":"+port, nil))
}

func envStringMust(k string) string {
	v := os.Getenv(k)
	if v == "" {
		fmt.Fprintf(os.Stderr, "$%s is required", k)
		os.Exit(1)
	}
	return v
}

func envInt(k string, d int) int {
	s := os.Getenv(k)
	if s == "" {
		return d
	}

	i, err := strconv.Atoi(s)
	if err != nil {
		return d
	}

	return i
}

func makeTimer(sec int) func() bool {
	var stop time.Time

	return func() bool {
		if stop.IsZero() {
			stop = time.Now().Add(time.Second * time.Duration(sec))
		}

		return time.Now().Before(stop)
	}
}

func testMySQL(url string, fn func() bool) error {
	return testSQLDB("mysql", url, fn)
}

func testPGSQL(url string, fn func() bool) error {
	return testSQLDB("postgres", "postgres://"+url, fn)
}

func testSQLDB(driver, url string, fn func() bool) error {
	db, err := sql.Open(driver, url)
	if err != nil {
		return err
	}

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS cf_monitoring (i INT)")
	if err != nil {
		return err
	}

	for i := 0; fn(); i++ {
		if _, err = db.Exec("INSERT INTO cf_monitoring VALUES (1)"); err != nil {
			return err
		}
	}

	if _, err = db.Exec("DROP TABLE cf_monitoring"); err != nil {
		return err
	}

	return nil
}

func testRedis(url string, fn func() bool) error {
	conn, err := redis.DialURL("redis://" + url)
	if err != nil {
		return err
	}
	defer conn.Close()

	for i := 0; fn(); i++ {
		key := "cf_monitoring-" + strconv.Itoa(i)

		if _, err := conn.Do("SET", key, i); err != nil {
			return err
		}
		if _, err := conn.Do("DEL", key); err != nil {
			return err
		}
	}

	return nil
}

func testMemcache(addr string, fn func() bool) error {
	mc := memcache.New(addr)

	b := make([]byte, unsafe.Sizeof(uint64(0)))
	for i := 0; fn(); i++ {
		binary.LittleEndian.PutUint64(b, uint64(i))

		if err := mc.Set(&memcache.Item{
			Key:   "cf_monitoring-" + strconv.Itoa(i),
			Value: b,
		}); err != nil {
			return err
		}

		if err := mc.Delete("cf_monitoring-" + strconv.Itoa(i)); err != nil {
			return err
		}
	}

	return nil
}

func testMongoDB(url string, fn func() bool) error {
	mg, err := mgo.Dial("mongodb://" + url)
	if err != nil {
		return err
	}
	defer mg.Close()

	c := mg.DB("").C("demo")

	for i := 0; fn(); i++ {
		if err = c.Insert(struct {
			I int
		}{i}); err != nil {
			return err
		}
	}

	return c.DropCollection()
}

func testCassandra(url string, fn func() bool) error {
	chunks := strings.SplitN(url, "/", 2)
	hosts := strings.Split(chunks[0], ",")

	cfg := gocql.NewCluster(hosts...)
	if len(chunks) == 2 {
		cfg.Keyspace = chunks[1]
	}

	sess, err := cfg.CreateSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	if err = sess.Query("CREATE TABLE IF NOT EXISTS cf_monitoring (id int PRIMARY KEY)").Exec(); err != nil {
		return err
	}

	for i := 0; fn(); i++ {
		if err = sess.Query("INSERT INTO cf_monitoring (id) VALUES (?)", i).Exec(); err != nil {
			return err
		}
	}

	if err = sess.Query("DROP TABLE cf_monitoring").Exec(); err != nil {
		return err
	}

	return nil
}

func testRabbitMQ(addr string, fn func() bool) error {
	conn, err := amqp.Dial("amqp://" + addr)
	if err != nil {
		return err
	}

	errCh := make(chan error, 2)
	doneCh := make(chan struct{})
	readCh := make(chan bool)

	// send
	go func() {
		defer close(readCh)

		ch, err := conn.Channel()
		if err != nil {
			errCh <- err
			return
		}

		q, err := ch.QueueDeclare("cf_monitoring", false, true, false, false, nil)
		if err != nil {
			errCh <- err
			return
		}

		for i := 0; fn(); i++ {
			err = ch.Publish("", q.Name, false, false, amqp.Publishing{
				ContentType: "text/plain",
				Body:        []byte{},
			})

			if err != nil {
				errCh <- err
				return
			}

			readCh<-true
		}
	}()

	// recv
	go func() {
		defer close(doneCh)

		ch, err := conn.Channel()
		if err != nil {
			errCh <- err
			return
		}

		q, err := ch.QueueDeclare("cf_monitoring", false, true, false, false, nil)
		if err != nil {
			errCh <- err
			return
		}

		for range readCh {
			_, err := ch.Consume(q.Name, "", true, false, false, false, nil)
			if err != nil {
				errCh <- err
				return
			}
		}
	}()

	select {
	case err := <- errCh:
		return err
	case <-doneCh:
		return nil
	}
}
