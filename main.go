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
		</div>

		<script src="//code.jquery.com/jquery-3.1.1.min.js" integrity="sha256-hVVnYaiADRTO2PzUGmuLJr8BLUSjGIZsDYGmIJLv2b8=" crossorigin="anonymous"></script>
		<script src="//maxcdn.bootstrapcdn.com/bootstrap/3.3.7/js/bootstrap.min.js" integrity="sha256-U5ZEeKfGNOja007MMD3YBI0A3OSZOQbeG6z2f2Y0hu8=" crossorigin="anonymous"></script>
	</body>
</html>
`)

func main() {
	mysqlURL := env("MYSQL_URL")
	pgsqlURL := env("PGSQL_URL")
	redisURL := env("REDIS_URL")
	memcacheAddr := env("MEMCACHE_ADDR")
	mongodbAddr := env("MONGODB_ADDR")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(indexHTML)
	})

	// MySQL
	http.HandleFunc("/mysql", func(w http.ResponseWriter, r *http.Request) {
		if err := testSQLDB("mysql", mysqlURL); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	})

	// PostgreSQL
	http.HandleFunc("/pgsql", func(w http.ResponseWriter, r *http.Request) {
		if err := testSQLDB("postgres", "postgres://"+pgsqlURL); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	})

	// Redis
	http.HandleFunc("/redis", func(w http.ResponseWriter, r *http.Request) {
		if err := testRedis(redisURL); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	})

	// Memcached
	http.HandleFunc("/memcache", func(w http.ResponseWriter, r *http.Request) {
		if err := testMemcache(memcacheAddr); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	})

	// MongoDB
	http.HandleFunc("/mongodb", func(w http.ResponseWriter, r *http.Request) {
		if err := testMongoDB(mongodbAddr); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	})

	// cf compatibility
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	panic(http.ListenAndServe(":"+port, nil))
}

func env(k string) string {
	v := os.Getenv(k)
	if v == "" {
		fmt.Fprintf(os.Stderr, "$%s is required", k)
		os.Exit(1)
	}
	return v
}

func testSQLDB(driver, source string) error {
	db, err := sql.Open(driver, source)
	if err != nil {
		return err
	}

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS cf_monitoring (i INT)")
	if err != nil {
		return err
	}

	for i := 0; i < 1000; i++ {
		if _, err = db.Exec("INSERT INTO cf_monitoring VALUES (1)"); err != nil {
			return err
		}
	}

	if _, err = db.Exec("DROP TABLE cf_monitoring"); err != nil {
		return err
	}

	return nil
}

func testRedis(url string) error {
	conn, err := redis.DialURL("redis://" + url)
	if err != nil {
		return err
	}
	defer conn.Close()

	for i := 0; i < 10000; i++ {
		key := "cf_monitoring-"+strconv.Itoa(i)

		if _, err := conn.Do("SET", key, i); err != nil {
			return err
		}
		if _, err := conn.Do("DEL", key); err != nil {
			return err
		}
	}

	return nil
}

func testMemcache(addr string) error {
	mc := memcache.New(addr)

	b := make([]byte, unsafe.Sizeof(uint64(0)))
	for i := 0; i < 10000; i++ {
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

func testMongoDB(addr string) error {
	mg, err := mgo.Dial(addr)
	if err != nil {
		return err
	}
	defer mg.Close()

	c := mg.DB("cf_monitoring").C("demo")

	for i := 0; i < 10000; i++ {
		if err = c.Insert(struct {
			I int
		}{i}); err != nil {
			return err
		}
	}

	return c.DropCollection()
}
