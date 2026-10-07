//! The web process: two pages, a websocket, and an endpoint that ENQUEUES.
//!
//! It never writes an entry itself. The button enqueues a job, the worker
//! records it and announces it, and this process relays that to every open
//! browser — which is the whole demonstration. A handler that inserted the row
//! and returned it would look identical on screen and prove nothing.

use actix_web::{web, App, HttpRequest, HttpResponse, HttpServer, Responder};
use apalis::prelude::*;
use apalis_sql::postgres::PostgresStorage;
use futures_util::StreamExt;
use seed::{Entry, RecordEntry, POLL_MS};
use sqlx::postgres::PgPoolOptions;
use tokio::sync::broadcast;

#[derive(Clone)]
struct State {
    db: seed::Db,
    queue: PostgresStorage<RecordEntry>,
    // ONE BROADCAST PER PROCESS, fed by the single watcher below. Every
    // websocket subscribes to it, so N browsers cost one poll rather than N.
    entries: broadcast::Sender<String>,
}

#[actix_web::main]
async fn main() -> std::io::Result<()> {
    // THE PORT COMES FROM THE ENVIRONMENT — see the manifest's `$PORT`. The
    // fallback is for running this outside nvoi, and it is NOT 8080: that port
    // belongs to the agent inside every sandbox, and a default nobody notices
    // is how it gets used anyway.
    let port: u16 = std::env::var("PORT")
        .ok()
        .and_then(|p| p.parse().ok())
        .unwrap_or(3000);

    // DIESEL FOR THIS APP'S DATA, and it runs the migrations as it connects.
    let db = seed::connect();

    // SQLX ONLY FOR THE QUEUE. apalis is built on it and owns its own tables;
    // nothing here reads `entries` through this pool.
    let queue_pool = PgPoolOptions::new()
        .connect(&seed::queue_url())
        .await
        .expect("queue");

    // Idempotent, and called in BOTH binaries: whichever starts first creates
    // them, and neither may assume it was the other.
    PostgresStorage::setup(&queue_pool).await.expect("queue");

    let queue = PostgresStorage::new(queue_pool);
    let (entries, _) = broadcast::channel::<String>(64);

    // ── THE ONE WATCHER ────────────────────────────────────────────────────
    //
    // This was a `PgListener` on a channel the worker `NOTIFY`d. SQLite has no
    // LISTEN/NOTIFY, so it polls for rows past the highest id it has seen and
    // fans them out in memory.
    //
    // ONE POLL PER PROCESS, not one per browser: every websocket subscribes to
    // the same broadcast, so an open page costs nothing.
    //
    // The demonstration is unchanged — a row written by a DIFFERENT process
    // appears in your browser. That was never a claim about how one process
    // nudged the other.
    {
        let tx = entries.clone();
        let db = db.clone();

        tokio::spawn(async move {
            // DIESEL IS SYNCHRONOUS, so every query goes through
            // `spawn_blocking`. Calling it directly here would park a tokio
            // worker thread for the duration of the query, and this loop runs
            // for the life of the process.
            let read = move |db: seed::Db, seen: i64| {
                tokio::task::spawn_blocking(move || seed::entries_after(&db, seen))
            };

            let start = {
                let db = db.clone();
                tokio::task::spawn_blocking(move || seed::last_entry_id(&db)).await
            };

            let mut seen: i64 = match start {
                Ok(Ok(id)) => id,
                // NOT a reason to give up: an empty table and a failed read are
                // both "nothing seen yet", and the next tick asks again.
                _ => 0,
            };

            loop {
                tokio::time::sleep(std::time::Duration::from_millis(POLL_MS)).await;

                let rows: Vec<Entry> = match read(db.clone(), seen).await {
                    Ok(Ok(rows)) => rows,
                    // A READ THAT FAILED IS NOT A ROW THAT DID NOT HAPPEN. The
                    // next tick asks again; advancing `seen` here would skip it
                    // forever.
                    _ => continue,
                };

                for entry in rows {
                    seen = entry.id;

                    if let Ok(payload) = serde_json::to_string(&entry) {
                        // A send with no subscribers is not an error — it is
                        // nobody having the page open, the ordinary case.
                        let _ = tx.send(payload);
                    }
                }
            }
        });
    }

    let state = State { db, queue, entries };

    HttpServer::new(move || {
        App::new()
            .app_data(web::Data::new(state.clone()))
            .route("/", web::get().to(index))
            .route("/about", web::get().to(about))
            .route("/ws", web::get().to(ws))
            .route("/api/entries", web::post().to(enqueue))
            // AFTER the routes above, because `Files` at "/static" would
            // otherwise be consulted for paths it has no business answering.
            .service(actix_files::Files::new("/static", "./public"))
    })
    .bind(("0.0.0.0", port))?
    .run()
    .await
}

async fn index(state: web::Data<State>) -> impl Responder {
    let db = state.db.clone();

    // `web::block` because diesel is synchronous — see the watcher above. A
    // handler that queried inline would block an actix worker thread.
    let entries: Vec<Entry> = web::block(move || seed::recent(&db))
        .await
        .expect("blocking")
        .unwrap_or_default();

    // THE SAME ROW THE SOCKET BUILDS, and that is not a detail.
    //
    // Two paths render an entry — this one on load, and `socket.onmessage` in
    // the template — so a row that arrives over the socket has to be
    // indistinguishable from one that was already there. It was not: this had no
    // classes at all and printed chrono's own `Display`, so the list rendered
    // flush against its border with a raw `2026-08-03 12:46:21.871650 UTC`,
    // and then the next entry to arrive was padded and formatted.
    let rows: String = entries
        .iter()
        .map(|e| {
            format!(
                r#"<li class="px-4 py-3 text-sm">Entry #{} <span class="text-muted-foreground">— {}</span></li>"#,
                e.id,
                e.created_at.format("%Y-%m-%d %H:%M:%S")
            )
        })
        .collect();

    HttpResponse::Ok()
        .content_type("text/html; charset=utf-8")
        .body(include_str!("../../templates/index.html").replace("<!--ENTRIES-->", &rows))
}

async fn about() -> impl Responder {
    HttpResponse::Ok()
        .content_type("text/html; charset=utf-8")
        .body(include_str!("../../templates/about.html"))
}

// ENQUEUE ONLY. 202, because the row does not exist yet and saying 201 would
// name something that has not been created.
async fn enqueue(state: web::Data<State>) -> impl Responder {
    let mut queue = state.queue.clone();

    match queue.push(RecordEntry).await {
        Ok(_) => HttpResponse::Accepted().json(serde_json::json!({"queued": true})),
        Err(error) => HttpResponse::InternalServerError()
            .json(serde_json::json!({"error": error.to_string()})),
    }
}

async fn ws(
    req: HttpRequest,
    body: web::Payload,
    state: web::Data<State>,
) -> actix_web::Result<HttpResponse> {
    let (response, mut session, mut stream) = actix_ws::handle(&req, body)?;
    let mut announcements = state.entries.subscribe();

    actix_web::rt::spawn(async move {
        loop {
            tokio::select! {
                // A finished job, relayed as it arrives.
                announced = announcements.recv() => match announced {
                    Ok(payload) => {
                        if session.text(payload).await.is_err() {
                            break;
                        }
                    }
                    Err(_) => break,
                },

                // NOTHING IS EXPECTED FROM THE BROWSER. This arm exists so the
                // close is noticed and the task ends — without it the loop
                // would hold a session for a tab that is gone.
                incoming = stream.next() => match incoming {
                    Some(Ok(actix_ws::Message::Close(_))) | None => break,
                    Some(Err(_)) => break,
                    _ => {}
                },
            }
        }

        let _ = session.close(None).await;
    });

    Ok(response)
}
