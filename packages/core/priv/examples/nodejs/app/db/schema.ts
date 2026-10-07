import { pgTable, text } from "drizzle-orm/pg-core";
import { sql } from "drizzle-orm";

export const users = pgTable("users", {
  id: text("id").primaryKey(), // UUID
  email: text("email").notNull(),

  // Email and password is the ONLY way in. The four OAuth providers this
  // started with are gone, and so are the columns that served them —
  // `provider`, `remote_id`, `access_token`, and four `anthropic_*` tokens.
  //
  // They were not harmless. `access_token` was `NOT NULL`, written when every
  // account came from a provider, so an email signup inserted NULL and died on
  // the constraint. A column nothing reads still decides whether a row can be
  // written.
  passwordHash: text("password_hash"), // bcrypt

  login: text("login"),
  name: text("name"),
  avatarUrl: text("avatar_url"),

  createdAt: text("created_at")
    .default(sql`CURRENT_TIMESTAMP`)
    .notNull(),
  lastLoginAt: text("last_login_at"),
});

export const systemUsers = pgTable("system_users", {
  id: text("id").primaryKey(), // UUID
  email: text("email").notNull(), // Email (unique)
  passwordHash: text("password_hash").notNull(), // bcrypt hash (required)
  name: text("name"),
  createdAt: text("created_at")
    .default(sql`CURRENT_TIMESTAMP`)
    .notNull(),
  updatedAt: text("updated_at")
    .default(sql`CURRENT_TIMESTAMP`)
    .notNull(),
});

export const workspaces = pgTable("workspaces", {
  id: text("id").primaryKey(), // UUID
  label: text("label").notNull(),
  createdAt: text("created_at")
    .default(sql`CURRENT_TIMESTAMP`)
    .notNull(),
  updatedAt: text("updated_at")
    .default(sql`CURRENT_TIMESTAMP`)
    .notNull(),
});

export const workspaceUsers = pgTable("workspace_users", {
  id: text("id").primaryKey(), // UUID
  workspaceId: text("workspace_id")
    .notNull()
    .references(() => workspaces.id, { onDelete: "cascade" }),
  userId: text("user_id")
    .notNull()
    .references(() => users.id, { onDelete: "cascade" }),
  createdAt: text("created_at")
    .default(sql`CURRENT_TIMESTAMP`)
    .notNull(),
  updatedAt: text("updated_at")
    .default(sql`CURRENT_TIMESTAMP`)
    .notNull(),
});

export const workspaceInvites = pgTable("workspace_invites", {
  id: text("id").primaryKey(), // UUID
  workspaceId: text("workspace_id")
    .notNull()
    .references(() => workspaces.id, { onDelete: "cascade" }),
  email: text("email").notNull(),
  userId: text("user_id").references(() => users.id, { onDelete: "cascade" }), // If invitee has account
  invitedByUserId: text("invited_by_user_id")
    .notNull()
    .references(() => users.id, { onDelete: "cascade" }),
  token: text("token").notNull().unique(),
  role: text("role").notNull(), // 'member' or 'admin'
  status: text("status").notNull(), // 'pending', 'accepted', 'declined', 'expired'
  expiresAt: text("expires_at").notNull(),
  acceptedAt: text("accepted_at"),
  createdAt: text("created_at")
    .default(sql`CURRENT_TIMESTAMP`)
    .notNull(),
  updatedAt: text("updated_at")
    .default(sql`CURRENT_TIMESTAMP`)
    .notNull(),
});

export const jobs = pgTable("jobs", {
  id: text("id").primaryKey(),
  userId: text("user_id")
    .notNull()
    .references(() => users.id),
  type: text("type").notNull(), // 'export', 'email', 'report'
  key: text("key").notNull(), // 'user-123:export'
  // TYPED AT THE SOURCE. This was a bare `text("status")`, so the schema said
  // `string` while three hand-written UI types said
  // `"queued" | "started" | "completed" | "error"` — they could never agree, and
  // every component that read a job from the database failed to typecheck.
  status: text("status", {
    enum: ["queued", "started", "completed", "error"],
  }).notNull(),
  payload: text("payload"),
  result: text("result"),
  error: text("error"),
  createdAt: text("created_at")
    .default(sql`CURRENT_TIMESTAMP`)
    .notNull(),
  startedAt: text("started_at"),
  completedAt: text("completed_at"),
});
