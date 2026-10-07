import { index, integer, pgTable, serial, text, timestamp } from "drizzle-orm/pg-core";

// Rows belong to a viewer by the id nvoi signs; there is no users table.
export const notes = pgTable(
  "notes",
  {
    id: serial("id").primaryKey(),
    viewerId: text("viewer_id").notNull(),
    body: text("body").notNull(),
    // Filled in by the count job in the jobs process.
    words: integer("words"),
    createdAt: timestamp("created_at").defaultNow().notNull(),
  },
  (table) => [index("notes_viewer_id").on(table.viewerId)],
);

// Messages waiting for the web process to send over its sockets. Any process
// may write here; only the web process holds sockets.
export const broadcasts = pgTable("broadcasts", {
  id: serial("id").primaryKey(),
  channel: text("channel").notNull(),
  data: text("data").notNull(),
  createdAt: timestamp("created_at").defaultNow().notNull(),
});

export type Note = typeof notes.$inferSelect;
