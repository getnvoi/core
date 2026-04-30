// Command web is the public site for the nvoi CLI. Single-binary
// Gin server, gorm-backed visit log on Postgres. Self-contained:
// nothing under internal/ or cmd/cli imports this package.
//
// Routes:
//
//	GET /         — log the hit, list every prior hit, render
//	GET /healthz  — 200 OK; for kube readiness/liveness probes
//
// Persistence: a single Postgres table, `visits(id, path, ua, at)`,
// connected via $DATABASE_URL injected by the deployment's
// per-service `secrets:` whitelist (resolved at the cmd/cli boundary
// from the operator's environment / .env). gorm/postgres uses the
// pgx driver under the hood — pure Go, CGO_ENABLED=0, distroless
// static final image.
package main

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Visit is the one row per hit on `/`. ID auto-increments via gorm's
// default primaryKey rule on uint. UA captures the User-Agent header
// at the time of the hit (truncated client-side by our store size if
// it ever grows beyond reason).
type Visit struct {
	ID   uint      `gorm:"primaryKey"`
	Path string    `gorm:"size:255;not null"`
	UA   string    `gorm:"size:512"`
	At   time.Time `gorm:"autoCreateTime;index"`
}

//go:embed templates/index.html
var templatesFS embed.FS

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required (set via service.secrets in nvoi.yaml)")
	}

	db, err := openDB(dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}

	if err := db.AutoMigrate(&Visit{}); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	tpl, err := template.ParseFS(templatesFS, "templates/index.html")
	if err != nil {
		log.Fatalf("parse template: %v", err)
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.SetHTMLTemplate(tpl)

	r.GET("/healthz", func(c *gin.Context) {
		// Cheap path — does NOT touch the DB. Liveness probes should
		// remain fast and side-effect-free.
		c.String(http.StatusOK, "ok")
	})

	r.GET("/", func(c *gin.Context) {
		v := Visit{Path: c.Request.URL.Path, UA: c.GetHeader("User-Agent")}
		if err := db.WithContext(c.Request.Context()).Create(&v).Error; err != nil {
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("insert visit: %w", err))
			return
		}
		var visits []Visit
		if err := db.WithContext(c.Request.Context()).
			Order("at DESC").
			Limit(200). // bound the page; oldest scrolls off
			Find(&visits).Error; err != nil {
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("list visits: %w", err))
			return
		}
		c.HTML(http.StatusOK, "index.html", gin.H{
			"Visits": visits,
			"Count":  len(visits),
			"Now":    time.Now().UTC().Format(time.RFC3339),
		})
	})

	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	log.Printf("nvoi web listening on %s", addr)
	if err := r.Run(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server: %v", err)
	}
}

// openDB connects to Postgres via the pgx driver gorm wraps. Logger
// silenced — gorm's default is chatty in production. Connection pool
// defaults are sane for a low-traffic public site; tune later if we
// see contention.
func openDB(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
}
