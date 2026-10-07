//! What diesel checks every query against.
//!
//! Hand-written rather than printed by `diesel print-schema`, which needs the
//! CLI and a database that already exists — neither of which is true when this
//! compiles. It is one table.
//!
//! It has to agree with `migrations/*/up.sql`, and nothing here enforces that:
//! the migration builds the table and this declares what diesel believes is in
//! it. They disagree LOUDLY though — a column named here and not created is a
//! runtime SQLite error on the first query, at boot, on every start.

diesel::table! {
    entries (id) {
        id -> BigInt,
        created_at -> Timestamp,
    }
}
