CREATE TABLE "broadcasts" (
	"id" serial PRIMARY KEY NOT NULL,
	"channel" text NOT NULL,
	"data" text NOT NULL,
	"created_at" timestamp DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "notes" (
	"id" serial PRIMARY KEY NOT NULL,
	"viewer_id" text NOT NULL,
	"body" text NOT NULL,
	"words" integer,
	"created_at" timestamp DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE INDEX "notes_viewer_id" ON "notes" USING btree ("viewer_id");