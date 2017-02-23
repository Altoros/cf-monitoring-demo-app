package main

import (
	"database/sql"
	"encoding/binary"
	"net/http"
	"os"
	"strconv"

	"unsafe"

	"github.com/bradfitz/gomemcache/memcache"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)

func main() {
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
		if err := sqldb("mysql", os.Getenv("MYSQL_URL")); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	})

	// PostgreSQL
	http.HandleFunc("/pgsql", func(w http.ResponseWriter, r *http.Request) {
		if err := sqldb("postgres", "postgres://"+os.Getenv("PGSQL_URL")); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	})

	// Memcached
	http.HandleFunc("/memcache", func(w http.ResponseWriter, r *http.Request) {
		if err := memcached(os.Getenv("MEMCACHE_ADDR")); err != nil {
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

func sqldb(driver, source string) error {
	db, err := sql.Open(driver, source)
	if err != nil {
		return err
	}

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS cf_monitoring_demo (i INT)")
	if err != nil {
		return err
	}

	for i := 0; i < 500; i++ {
		if _, err = db.Exec("INSERT INTO cf_monitoring_demo VALUES (1)"); err != nil {
			return err
		}
	}

	if _, err = db.Exec("DROP TABLE cf_monitoring_demo"); err != nil {
		return err
	}

	return nil
}

func memcached(addr string) (err error) {
	mc := memcache.New(addr)

	b := make([]byte, unsafe.Sizeof(uint64(0)))
	for i := 0; i < 10000; i++ {
		binary.LittleEndian.PutUint64(b, uint64(i))

		if err = mc.Set(&memcache.Item{
			Key:   "cf_monitoring-" + strconv.Itoa(i),
			Value: b,
		}); err != nil {
			return
		}
	}

	// cleanup
	for i := 0; i < 10000; i++ {
		if err = mc.Delete("cf_monitoring-" + strconv.Itoa(i)); err != nil {
			return
		}
	}

	return
}

var indexHTML = []byte(`
<!DOCTYPE html>
<html>
	<head>
		<link rel="stylesheet" href="//maxcdn.bootstrapcdn.com/bootstrap/3.3.7/css/bootstrap.min.css" integrity="sha256-916EbMg70RQy9LHiGkXzG8hSg9EdNy97GazNG/aiY1w=" crossorigin="anonymous">
		<title>CERTS</title>
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
			<a class="btn btn-info" href="/memcache">Memcache</a>
		</div>

		<script src="//code.jquery.com/jquery-3.1.1.min.js" integrity="sha256-hVVnYaiADRTO2PzUGmuLJr8BLUSjGIZsDYGmIJLv2b8=" crossorigin="anonymous"></script>
		<script src="//maxcdn.bootstrapcdn.com/bootstrap/3.3.7/js/bootstrap.min.js" integrity="sha256-U5ZEeKfGNOja007MMD3YBI0A3OSZOQbeG6z2f2Y0hu8=" crossorigin="anonymous"></script>
	</body>
</html>
`)