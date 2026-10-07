// The web process: two pages, a websocket, and an endpoint that ENQUEUES.
//
// It never writes an entry itself. The button enqueues a job, the worker records
// it and announces it, and this process relays that to every open browser —
// which is the whole demonstration. A handler that inserted the row and returned
// it would look identical on screen and prove nothing.
package main

import (
	"context"
	"embed"
	"html/template"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"nvoi.to/seed/internal/seed"
)

//go:embed templates/*.html
var templates embed.FS

// EVERY CONNECTED BROWSER. A map behind a mutex rather than a channel fan-out:
// the set changes on connect and disconnect and is read on every announcement,
// which is exactly what a mutex is for.
type hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]bool
}

func (h *hub) add(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
}

func (h *hub) drop(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
	c.Close()
}

// A WRITE THAT FAILS DROPS THE CLIENT. A browser that closed its tab is not an
// error to report; it is a connection to forget. Keeping it would grow the map
// forever and block every later announcement on a socket nobody reads.
func (h *hub) send(payload string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for c := range h.clients {
		if err := c.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
			delete(h.clients, c)
			c.Close()
		}
	}
}

func main() {
	ctx := context.Background()

	// THE PORT COMES FROM THE ENVIRONMENT — see the manifest's `$PORT`.
	//
	// The fallback is for running this outside nvoi, and it is NOT 8080: that
	// port belongs to the agent inside every sandbox, and a default nobody
	// notices is how it gets used anyway.
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	pool, err := seed.Pool(ctx)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	// BOTH BINARIES MIGRATE, and neither may assume the other started first.
	// They are separate services and the manifest's order is a preference, not a
	// barrier — so this is idempotent and runs in whichever comes up.
	if err := seed.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	// INSERT-ONLY. This client never works a job — `Workers` is absent on
	// purpose, so the web process cannot quietly become the worker if somebody
	// registers one here later.
	queue, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		log.Fatalf("queue: %v", err)
	}

	h := &hub{clients: map[*websocket.Conn]bool{}}

	go listen(ctx, pool, h)

	tmpl := template.Must(template.ParseFS(templates, "templates/*.html"))

	r := gin.Default()

	// ── THE STYLESHEET, FROM DISK RATHER THAN EMBEDDED ─────────────────────
	//
	// `public/app.css` is written by the `css` process and rewritten every time
	// a class changes. Embedding it would freeze whatever existed at BUILD time,
	// so editing a template would change nothing until the binary was rebuilt —
	// which is the opposite of what a watcher is for.
	//
	// The templates are embedded because they are compiled into the handlers;
	// the stylesheet is not, because it is regenerated while the server runs.
	r.Static("/static", "./public")

	// ── ROUTING ────────────────────────────────────────────────────────────
	//
	// Two pages, because "it serves something" and "it routes" are different
	// claims and only the second needs more than one route.
	r.GET("/", func(c *gin.Context) {
		entries, err := recent(c.Request.Context(), pool)
		if err != nil {
			c.String(http.StatusInternalServerError, "could not read entries: %v", err)
			return
		}

		render(c, tmpl, "index.html", gin.H{"Entries": entries})
	})

	r.GET("/about", func(c *gin.Context) {
		render(c, tmpl, "about.html", gin.H{"Runtime": "Go · Gin · Huma · gorilla/websocket · River"})
	})

	// ── THE WEBSOCKET ──────────────────────────────────────────────────────
	//
	// `CheckOrigin` accepts everything because a preview is served from a
	// hostname this process never learns — `<worktree>-preview-dev.nvoi.to` —
	// and a seed that refused it would be a websocket demo that cannot connect.
	// A real application replaces this with its own origins.
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	r.GET("/ws", func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}

		h.add(conn)

		// READ UNTIL IT CLOSES. Nothing is expected FROM the browser; this loop
		// exists so the close is noticed and the client dropped.
		go func() {
			defer h.drop(conn)

			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
	})

	// ── THE API, DESCRIBED ─────────────────────────────────────────────────
	//
	// Huma over the same Gin router, so the OpenAPI document describes what is
	// actually served rather than a second app beside it.
	api := humagin.New(r, huma.DefaultConfig("Seed", "1.0.0"))

	huma.Register(api, huma.Operation{
		OperationID:   "enqueue-entry",
		Method:        http.MethodPost,
		Path:          "/api/entries",
		Summary:       "Ask for an entry to be recorded",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, _ *struct{}) (*struct {
		Body struct {
			Queued bool `json:"queued"`
		}
	}, error) {
		if _, err := queue.Insert(ctx, seed.RecordEntry{}, nil); err != nil {
			return nil, huma.Error500InternalServerError("could not enqueue", err)
		}

		// 202, because the row does not exist yet. Saying 201 would name
		// something that has not been created.
		out := &struct {
			Body struct {
				Queued bool `json:"queued"`
			}
		}{}
		out.Body.Queued = true

		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-entries",
		Method:      http.MethodGet,
		Path:        "/api/entries",
		Summary:     "Every entry, newest first",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []seed.Entry }, error) {
		entries, err := recent(ctx, pool)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read entries", err)
		}

		return &struct{ Body []seed.Entry }{Body: entries}, nil
	})

	log.Printf("listening on :%s", port)

	if err := r.Run("0.0.0.0:" + port); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

// THE ONE LISTENER. Announcements arrive on a dedicated connection and fan out
// in memory, so N browsers cost one LISTEN rather than N connections.
func listen(ctx context.Context, pool *pgxpool.Pool, h *hub) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		log.Printf("listen: %v", err)
		return
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "listen "+seed.Channel); err != nil {
		log.Printf("listen: %v", err)
		return
	}

	for {
		note, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			log.Printf("listen: %v", err)
			return
		}

		h.send(note.Payload)
	}
}

func render(c *gin.Context, tmpl *template.Template, name string, data gin.H) {
	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/html; charset=utf-8")

	if err := tmpl.ExecuteTemplate(c.Writer, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func recent(ctx context.Context, pool *pgxpool.Pool) ([]seed.Entry, error) {
	rows, err := pool.Query(ctx, "select id, created_at from entries order by id desc limit 50")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// `[]Entry{}`, NOT a nil slice: this is serialised to JSON, and nil encodes
	// as `null` where the browser is iterating an array.
	entries := []seed.Entry{}

	for rows.Next() {
		var entry seed.Entry
		if err := rows.Scan(&entry.ID, &entry.CreatedAt); err != nil {
			return nil, err
		}

		entries = append(entries, entry)
	}

	return entries, rows.Err()
}
