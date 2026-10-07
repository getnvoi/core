//! What both binaries need: the row, the job, and how a worker tells the web
//! process that one happened.
//!
//! ## The two processes talk through the DATABASE, not through each other
//!
//! `web` enqueues and `worker` runs the job — separate processes, so there is no
//! shared memory to broadcast over.
//!
//! ## The worker no longer ANNOUNCES; the web process WATCHES
//!
//! This used `NOTIFY entries` with every web process `LISTEN`ing, which SQLite
//! has no equivalent for. The web process polls for rows newer than the last one
//! it saw and fans them out in memory instead.
//!
//! The demonstration is unchanged and it is worth being clear about why: the
//! claim was never "Postgres can push". It is that a row written by a DIFFERENT
//! process appears in your browser — which is about two processes sharing a
//! database, not about how one nudges the other.
//!
//! An in-process channel would be simpler and would only work while both halves
//! were one program, which is exactly what the services pattern splits apart.
//!
//! ## TWO LIBRARIES REACH THIS FILE, AND THAT IS ON PURPOSE
//!
//! **diesel owns the table.** The model, the migration and every query about an
//! entry are diesel's, and the migrations are embedded so they run at boot.
//!
//! **sqlx exists only because apalis is built on it.** `apalis-sql` takes a
//! `PgPool` — it owns its own tables and speaks its own SQL, exactly as
//! goqite does in the Go seed. It is the queue's private connection, not a
//! second way to read this app's data. Nothing here queries `entries` through
//! it.

pub mod schema;

use diesel::prelude::*;
use diesel::r2d2::{ConnectionManager, Pool, PooledConnection};
use diesel_migrations::{embed_migrations, EmbeddedMigrations, MigrationHarness};
use serde::{Deserialize, Serialize, Serializer};

/// How often the web process looks for rows it has not seen.
///
/// Fast enough to feel immediate, slow enough that an idle page is not a query
/// per frame. There is no push to wait on — see the module docs.
pub const POLL_MS: u64 = 400;

/// The migrations, compiled INTO the binary.
///
/// A container has no `diesel` CLI and no `migrations/` directory at run time —
/// only the binary. Embedding them is what makes `run_migrations` a call rather
/// than a step somebody has to remember to put in the manifest.
pub const MIGRATIONS: EmbeddedMigrations = embed_migrations!("migrations");

pub type Db = Pool<ConnectionManager<PgConnection>>;

/// One row, and the shape sent to a browser.
#[derive(Debug, Clone, Queryable, Selectable, Serialize, Deserialize)]
#[diesel(table_name = schema::entries)]
#[diesel(check_for_backend(diesel::pg::Pg))]
pub struct Entry {
    pub id: i64,
    // UTC, WITH THE `Z`, because the browser does `new Date(entry.created_at)`
    // and a naive string is parsed as LOCAL time — so an entry would appear
    // hours off for anyone not on UTC. diesel's SQLite `Timestamp` is naive, so
    // the marker is added on the way out rather than assumed.
    #[serde(serialize_with = "as_utc")]
    pub created_at: chrono::NaiveDateTime,
}

fn as_utc<S: Serializer>(at: &chrono::NaiveDateTime, s: S) -> Result<S::Ok, S::Error> {
    s.serialize_str(&at.format("%Y-%m-%dT%H:%M:%S%.3fZ").to_string())
}

/// The row on the way IN.
///
/// The timestamp is set here rather than defaulted in SQL, so the one place a
/// row is defined is the one place it is written.
#[derive(Insertable)]
#[diesel(table_name = schema::entries)]
pub struct NewEntry {
    pub created_at: chrono::NaiveDateTime,
}

impl NewEntry {
    pub fn now() -> Self {
        Self {
            created_at: chrono::Utc::now().naive_utc(),
        }
    }
}

/// The job. It carries nothing: what it does is "record an entry", and a
/// payload would be a fact the web process guessed on the worker's behalf.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RecordEntry;

/// `DATABASE_PATH`, whole, with no assembly and no default.
///
/// `DATABASE_URL`, whole, with no assembly and no default.
///
/// nvoi mints the connection and writes it out in every shape a client might
/// look for. This reads the URL because that is the one spelling both diesel
/// and sqlx take without argument — and rebuilding one from the parts loses
/// `sslmode` and the endpoint parameter a managed provider routes on, which
/// fails as a connection timeout that reads as a network problem.
///
/// Missing is FATAL. An app with no database cannot serve, and a process that
/// exits saying so is worth more than one that connects somewhere nobody looks.
pub fn database_url() -> String {
    std::env::var("DATABASE_URL")
        .expect("DATABASE_URL is not set: this app cannot run without a database")
}

/// The URL **apalis** wants — the same one, because there is one database.
pub fn queue_url() -> String {
    database_url()
}

// THERE ARE NO PRAGMAS HERE, and that is the difference this file is about.
//
// The SQLite twin sets `journal_mode = wal` and `busy_timeout` on every
// connection, because two processes writing one FILE block each other and the
// loser must wait rather than fail. A server has none of that: concurrency is
// its job, and there is no file for two processes to contend over.

/// The pool both binaries share, with the schema already brought up to date.
///
/// MIGRATIONS RUN HERE rather than in a setup step: both binaries call this, it
/// is idempotent, and neither may assume the other started first.
pub fn connect() -> Db {
    let url = database_url();

    let pool = Pool::builder()
        // ── SMALL ON PURPOSE ────────────────────────────────────────────────
        //
        // The SQLite twin sizes this against a file and a pool of one deadlocked
        // it. Here the constraint is the other way round: a managed provider
        // COUNTS connections, and this process already opens a second pool for
        // apalis. Five is enough for actix's workers and the watcher without
        // spending somebody's quota on idle sockets.
        .max_size(5)
        .build(ConnectionManager::<PgConnection>::new(url))
        .expect("database");

    pool.get()
        .expect("database")
        .run_pending_migrations(MIGRATIONS)
        .expect("migrations");

    pool
}

pub fn conn(db: &Db) -> PooledConnection<ConnectionManager<PgConnection>> {
    db.get().expect("database")
}

/// The most recent entries, newest first — what a page shows on load.
pub fn recent(db: &Db) -> QueryResult<Vec<Entry>> {
    use schema::entries::dsl;

    dsl::entries
        .select(Entry::as_select())
        .order(dsl::id.desc())
        .limit(50)
        .load(&mut conn(db))
}

/// Everything written since `seen` — what the watcher asks for on every tick.
pub fn entries_after(db: &Db, seen: i64) -> QueryResult<Vec<Entry>> {
    use schema::entries::dsl;

    dsl::entries
        .select(Entry::as_select())
        .filter(dsl::id.gt(seen))
        .order(dsl::id.asc())
        .load(&mut conn(db))
}

/// The highest id already written, which is where a fresh watcher starts.
pub fn last_entry_id(db: &Db) -> QueryResult<i64> {
    use schema::entries::dsl;

    dsl::entries
        .select(diesel::dsl::max(dsl::id))
        .first::<Option<i64>>(&mut conn(db))
        // `max` over an empty table is SQL NULL, which is a real answer — the
        // table is empty — not a missing one. Zero is below every rowid.
        .map(|highest| highest.unwrap_or(0))
}

/// Record an entry. The worker's whole job.
pub fn record_entry(db: &Db) -> QueryResult<usize> {
    diesel::insert_into(schema::entries::table)
        .values(NewEntry::now())
        .execute(&mut conn(db))
}
