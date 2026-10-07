CREATE TABLE "users" (
	"id" text PRIMARY KEY NOT NULL,
	"email" text NOT NULL,
	"password_hash" text,
	"login" text,
	"name" text,
	"avatar_url" text,
	"created_at" text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	"last_login_at" text
);
--> statement-breakpoint
CREATE TABLE "system_users" (
	"id" text PRIMARY KEY NOT NULL,
	"email" text NOT NULL,
	"password_hash" text NOT NULL,
	"name" text,
	"created_at" text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	"updated_at" text DEFAULT CURRENT_TIMESTAMP NOT NULL
);
--> statement-breakpoint
CREATE TABLE "workspaces" (
	"id" text PRIMARY KEY NOT NULL,
	"label" text NOT NULL,
	"created_at" text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	"updated_at" text DEFAULT CURRENT_TIMESTAMP NOT NULL
);
--> statement-breakpoint
CREATE TABLE "workspace_users" (
	"id" text PRIMARY KEY NOT NULL,
	"workspace_id" text NOT NULL,
	"user_id" text NOT NULL,
	"created_at" text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	"updated_at" text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	CONSTRAINT "workspace_users_workspace_id_fk" FOREIGN KEY ("workspace_id") REFERENCES "workspaces"("id") ON DELETE cascade,
	CONSTRAINT "workspace_users_user_id_fk" FOREIGN KEY ("user_id") REFERENCES "users"("id") ON DELETE cascade
);
--> statement-breakpoint
CREATE TABLE "workspace_invites" (
	"id" text PRIMARY KEY NOT NULL,
	"workspace_id" text NOT NULL,
	"email" text NOT NULL,
	"user_id" text,
	"invited_by_user_id" text NOT NULL,
	"token" text NOT NULL,
	"role" text NOT NULL,
	"status" text NOT NULL,
	"expires_at" text NOT NULL,
	"accepted_at" text,
	"created_at" text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	"updated_at" text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	CONSTRAINT "workspace_invites_workspace_id_fk" FOREIGN KEY ("workspace_id") REFERENCES "workspaces"("id") ON DELETE cascade,
	CONSTRAINT "workspace_invites_user_id_fk" FOREIGN KEY ("user_id") REFERENCES "users"("id") ON DELETE cascade,
	CONSTRAINT "workspace_invites_invited_by_user_id_fk" FOREIGN KEY ("invited_by_user_id") REFERENCES "users"("id") ON DELETE cascade
);
--> statement-breakpoint
CREATE TABLE "jobs" (
	"id" text PRIMARY KEY NOT NULL,
	"user_id" text NOT NULL,
	"type" text NOT NULL,
	"key" text NOT NULL,
	"status" text NOT NULL,
	"payload" text,
	"result" text,
	"error" text,
	"created_at" text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	"started_at" text,
	"completed_at" text,
	CONSTRAINT "jobs_user_id_fk" FOREIGN KEY ("user_id") REFERENCES "users"("id")
);
