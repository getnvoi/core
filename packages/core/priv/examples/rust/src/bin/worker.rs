//! The jobs process: takes work off the queue, records the entry, announces it.
//!
//! It is its OWN process, started and followed separately — the same shape as
//! Rails' `bin/jobs`. That is the point being demonstrated: the row a browser
//! sees appears because something else ran, not because the request that
//! triggered it returned a value.

use apalis::prelude::*;
use apalis_sql::postgres::PostgresStorage;
use seed::RecordEntry;
use sqlx::postgres::PgPoolOptions;

async fn record(_job: RecordEntry, db: Data<seed::Db>) -> Result<(), Error> {
    let db = (*db).clone();

    // DIESEL IS SYNCHRONOUS, so the insert goes to a blocking thread rather
    // than parking a tokio worker.
    tokio::task::spawn_blocking(move || seed::record_entry(&db))
        .await
        .map_err(|e| Error::Failed(std::sync::Arc::new(Box::new(e))))?
        .map_err(|e| Error::Failed(std::sync::Arc::new(Box::new(e))))?;

    // ── THE ROW IS THE ANNOUNCEMENT ────────────────────────────────────────
    //
    // This serialised the entry and published it on a Postgres channel every web
    // process was `LISTEN`ing on. SQLite has no LISTEN/NOTIFY, so the web
    // process watches the table for rows past the last id it saw.
    //
    // Nothing is lost by writing and stopping: the demonstration is that a row
    // written by a DIFFERENT process reaches a browser, which is about two
    // processes sharing a database rather than how one nudges the other.
    Ok(())
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // DIESEL FOR THIS APP'S DATA, and it runs the migrations as it connects —
    // in BOTH binaries, because neither may assume the other started first.
    let db = seed::connect();

    // SQLX ONLY FOR THE QUEUE, whose tables are its own.
    let queue_pool = PgPoolOptions::new().connect(&seed::queue_url()).await?;
    PostgresStorage::setup(&queue_pool).await?;

    let storage: PostgresStorage<RecordEntry> = PostgresStorage::new(queue_pool);

    Monitor::new()
        .register(
            WorkerBuilder::new("record-entry")
                .data(db)
                .backend(storage)
                .build_fn(record),
        )
        .run()
        .await?;

    Ok(())
}
