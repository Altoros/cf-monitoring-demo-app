package main

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"unsafe"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/garyburd/redigo/redis"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gocql/gocql"
	_ "github.com/lib/pq"
	"gopkg.in/mgo.v2"
)

var indexHTML = []byte(`
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
			<a class="btn btn-primary" href="/mysql">MySQL</a>
			<a class="btn btn-success" href="/pgsql">PostgreSQL</a>
			<a class="btn btn-danger" href="/redis">Redis</a>
			<a class="btn btn-info" href="/memcache">Memcache</a>
			<a class="btn btn-warning" href="/mongodb">MongoDB</a>
			<a class="btn btn-default" href="/cassandra">Cassandra</a>
		</div>

		<script src="//code.jquery.com/jquery-3.1.1.min.js" integrity="sha256-hVVnYaiADRTO2PzUGmuLJr8BLUSjGIZsDYGmIJLv2b8=" crossorigin="anonymous"></script>
		<script src="//maxcdn.bootstrapcdn.com/bootstrap/3.3.7/js/bootstrap.min.js" integrity="sha256-U5ZEeKfGNOja007MMD3YBI0A3OSZOQbeG6z2f2Y0hu8=" crossorigin="anonymous"></script>
	</body>
</html>
`)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(indexHTML)
	})

	for path, s := range map[string]*struct {
		fn   func(string, int) error
		addr string
		num  int
	}{
		"/mysql":     {testMySQL, envStringMust("MYSQL_URL"), envInt("MYSQL_NUM", 1000)},
		"/pgsql":     {testPGSQL, envStringMust("PGSQL_URL"), envInt("PGSQL_NUM", 1000)},
		"/redis":     {testRedis, envStringMust("REDIS_URL"), envInt("REDIS_NUM", 10000)},
		"/memcache":  {testMemcache, envStringMust("MEMCACHE_ADDR"), envInt("MEMCACHE_NUM", 10000)},
		"/mongodb":   {testMongoDB, envStringMust("MONGODB_URL"), envInt("MONGODB_NUM", 10000)},
		"/cassandra": {testCassandra, envStringMust("CASSANDRA_HOST"), envInt("CASSANDRA_NUM", 10000)},
	} {
		s := s

		http.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if err := s.fn(s.addr, s.num); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
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

func testMySQL(url string, num int) error {
	return testSQLDB("mysql", url, num)
}

func testPGSQL(url string, num int) error {
	return testSQLDB("postgres", "postgres://"+url, num)
}

func testSQLDB(driver, url string, num int) error {
	db, err := sql.Open(driver, url)
	if err != nil {
		return err
	}

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS cf_monitoring (i INT)")
	if err != nil {
		return err
	}

	for i := 0; i < num; i++ {
		if _, err = db.Exec("INSERT INTO cf_monitoring VALUES (1)"); err != nil {
			return err
		}
	}

	if _, err = db.Exec("DROP TABLE cf_monitoring"); err != nil {
		return err
	}

	return nil
}

func testRedis(url string, num int) error {
	conn, err := redis.DialURL("redis://" + url)
	if err != nil {
		return err
	}
	defer conn.Close()

	for i := 0; i < num; i++ {
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

func testMemcache(addr string, num int) error {
	mc := memcache.New(addr)

	b := make([]byte, unsafe.Sizeof(uint64(0)))
	for i := 0; i < num; i++ {
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

func testMongoDB(url string, num int) error {
	mg, err := mgo.Dial("mongodb://" + url)
	if err != nil {
		return err
	}
	defer mg.Close()

	c := mg.DB("").C("demo")

	for i := 0; i < num; i++ {
		if err = c.Insert(struct {
			I int
		}{i}); err != nil {
			return err
		}
	}

	return c.DropCollection()
}

func testCassandra(host string, num int) error {
	cfg := gocql.NewCluster(host)

	sess, err := cfg.CreateSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	if err := sess.Query("CREATE TABLE IF NOT EXISTS cf_monitoring (i INT)").Exec(); err != nil {
		return err
	}

	for i := 0; i < num; i++ {
		if err = sess.Query("INSERT INTO cf_monitoring VALUES (?)", i).Exec(); err != nil {
			return err
		}
	}

	if err = sess.Query("DROP TABLE cf_monitoring").Exec(); err != nil {
		return err
	}

	return nil
}
