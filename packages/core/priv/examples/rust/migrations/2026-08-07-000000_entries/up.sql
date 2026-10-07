-- The one table this seed owns.
--
-- EMBEDDED IN THE BINARY and run at boot by both processes, so the schema
-- travels with the code that expects it. There is no schema step in the
-- catalogue and no `db/schema.sql` to keep in sync with `src/schema.rs`.
--
-- `bigserial` rather than SQLite's `integer primary key` rowid alias. The
-- timestamp is written by the application rather than defaulted here, because
-- diesel's `Insertable` is where the row is defined.
create table entries (
  id bigserial primary key,
  created_at timestamp not null
);
