defmodule Nvoi.Postgres.Migrations.OwnSchema do
  @moduledoc """
  The schema under the server's own names, with nothing left of the framework the database was first made with:

  - `schema_migrations` (that framework's versions) and `ar_internal_metadata` are dropped: nothing reads them. The
    migrations' versions are in `ecto_migrations`.
  - A note's body moves from `action_text_rich_texts` (one row per note, `record_type` "Event", `name` "body") into
    its event's `content`, and the table is dropped.
  - `logs.loggable_type` and `loggable_id` become `record_type` and `record_id`, as on `attachments`.
  - Foreign keys named `fk_rails_<hash>` take Postgres' own name, `<table>_<column>_fkey`.
  - The not-null constraints Postgres 18 named after a table's or a column's former name (`active_storage_*`,
    `deployments_*` on `provisionings`, `logs_loggable_*`) take `<table>_<column>_not_null`.

  A constraint missing from a database is skipped.

  Down puts each back; the dropped versions are not, since nothing reads them.
  """
  use Ecto.Migration

  @foreign_keys [
    {"agent_session_states", "workspace_id", "fk_rails_06b1adc62f"},
    {"agent_sessions", "agent_id", "fk_rails_b85c46aa04"},
    {"agent_sessions", "agent_session_state_id", "fk_rails_599a10d2ea"},
    {"agent_sessions", "llm_config_id", "fk_rails_d9f9a129c1"},
    {"agent_sessions", "parent_id", "fk_rails_26094d0178"},
    {"agent_sessions", "task_id", "fk_rails_a578c773a0"},
    {"agent_sessions", "user_id", "fk_rails_17e340e3c5"},
    {"agent_sessions", "workflow_id", "fk_rails_e0e01956b6"},
    {"agent_sessions", "workflow_step_id", "fk_rails_249b97a4b5"},
    {"agent_sessions", "workspace_id", "fk_rails_5823d75385"},
    {"agent_sessions", "worktree_id", "fk_rails_c65b45f0f2"},
    {"agent_skills", "agent_id", "fk_rails_844ca0e9d6"},
    {"agent_skills", "skill_id", "fk_rails_e0d0257138"},
    {"agents", "llm_config_id", "fk_rails_d711598cbf"},
    {"agents", "workspace_id", "fk_rails_f9d68ffa97"},
    {"artifact_versions", "artifact_id", "fk_rails_24489ca950"},
    {"artifact_versions", "turn_id", "fk_rails_2588b5f86d"},
    {"artifact_versions", "user_id", "fk_rails_c1607d68ad"},
    {"artifacts", "session_id", "fk_rails_1d6c9d3577"},
    {"artifacts", "workspace_id", "fk_rails_dd6244b558"},
    {"artifacts", "worktree_id", "fk_rails_2e597c9782"},
    {"captures", "turn_id", "fk_rails_573a6e9f46"},
    {"connectors", "installer_id", "fk_rails_076f34eb6d"},
    {"connectors", "workspace_id", "fk_rails_e9e7b0a1d0"},
    {"databases", "environment_id", "fk_rails_fdd153d25f"},
    {"databases", "parent_id", "fk_rails_be60a7c73b"},
    {"databases", "worktree_id", "fk_rails_510fa9ee69"},
    {"provisionings", "worktree_id", "fk_rails_eb90d9ddd0"},
    {"env_vars", "environment_id", "fk_rails_cb963c2dcd"},
    {"environment_commands", "environment_id", "fk_rails_2d2471578a"},
    {"environments", "repo_id", "fk_rails_e45a4848d1"},
    {"events", "agent_session_id", "fk_rails_70f1c83281"},
    {"events", "parent_id", "fk_rails_68f023eb25"},
    {"events", "turn_id", "fk_rails_7730d2249d"},
    {"events", "user_id", "fk_rails_0cb5590091"},
    {"github_install_intents", "user_id", "fk_rails_2f40279d9e"},
    {"github_install_intents", "workspace_id", "fk_rails_b83f699abb"},
    {"github_repositories", "github_installation_id", "fk_rails_4b5b7ee569"},
    {"grants", "environment_id", "fk_rails_43f4f4a8b7"},
    {"grants", "workspace_user_id", "fk_rails_0af7a62209"},
    {"library_items", "event_id", "fk_rails_d499756529"},
    {"library_items", "workspace_id", "fk_rails_386fa9f5ad"},
    {"llm_configs", "workspace_id", "fk_rails_b749224e02"},
    {"llm_connections", "llm_config_id", "fk_rails_cf5431baba"},
    {"llm_connections", "user_id", "fk_rails_8d1ef5f2a9"},
    {"llm_connections", "workspace_id", "fk_rails_8bf0ecd8a8"},
    {"note_mentions", "event_id", "fk_rails_bdf7f38a1f"},
    {"note_mentions", "user_id", "fk_rails_7b5fdb5b3f"},
    {"repos", "github_installation_id", "fk_rails_1ea9f67259"},
    {"repos", "workspace_id", "fk_rails_ebde1c1667"},
    {"seeds", "environment_id", "fk_rails_79700c5200"},
    {"session_presences", "agent_session_id", "fk_rails_6b1dbea869"},
    {"session_presences", "user_id", "fk_rails_54e017fc0b"},
    {"sessions", "user_id", "fk_rails_758836b4f0"},
    {"sessions", "workspace_id", "fk_rails_85704b4d26"},
    {"skill_files", "skill_id", "fk_rails_6bcf1ee1d9"},
    {"skills", "workspace_id", "fk_rails_b610ca2166"},
    {"tasks", "agent_id", "fk_rails_ffa0a54871"},
    {"tasks", "environment_id", "fk_rails_58920e259f"},
    {"tasks", "workflow_id", "fk_rails_025c576ee7"},
    {"tasks", "workspace_id", "fk_rails_e21c9f6818"},
    {"thumbnails", "workspace_id", "fk_rails_b557a2206c"},
    {"tool_grants", "workspace_user_id", "fk_rails_5be17306a5"},
    {"tool_presets", "workspace_id", "fk_rails_1c18a2231d"},
    {"turns", "agent_session_id", "fk_rails_2e723a423e"},
    {"turns", "lead_id", "fk_rails_68cd541ff9"},
    {"turns", "note_id", "fk_rails_aa182dd478"},
    {"turns", "user_id", "fk_rails_b6b8f671e0"},
    {"vm_costs", "workspace_id", "fk_rails_12272edfd2"},
    {"vm_snapshots", "workspace_id", "fk_rails_317523178e"},
    {"workflow_steps", "agent_id", "fk_rails_45755eb128"},
    {"workflow_steps", "agent_session_state_id", "fk_rails_2c60628e35"},
    {"workflow_steps", "workflow_id", "fk_rails_af7ad8555a"},
    {"workflows", "environment_id", "fk_rails_f5f8b2cf3e"},
    {"workflows", "workspace_id", "fk_rails_f64cd10465"},
    {"workspace_users", "user_id", "fk_rails_f426ec61c5"},
    {"workspace_users", "workspace_id", "fk_rails_9ce96516d3"},
    {"worktrees", "environment_id", "fk_rails_492a66d4a7"},
    {"worktrees", "repo_id", "fk_rails_aa6b05c16f"}
  ]

  @not_null [
    {"attachments", "blob_id", "active_storage_attachments_blob_id_not_null"},
    {"attachments", "created_at", "active_storage_attachments_created_at_not_null"},
    {"attachments", "id", "active_storage_attachments_id_not_null"},
    {"attachments", "name", "active_storage_attachments_name_not_null"},
    {"attachments", "record_id", "active_storage_attachments_record_id_not_null"},
    {"attachments", "record_type", "active_storage_attachments_record_type_not_null"},
    {"blobs", "byte_size", "active_storage_blobs_byte_size_not_null"},
    {"blobs", "created_at", "active_storage_blobs_created_at_not_null"},
    {"blobs", "filename", "active_storage_blobs_filename_not_null"},
    {"blobs", "id", "active_storage_blobs_id_not_null"},
    {"blobs", "key", "active_storage_blobs_key_not_null"},
    {"blobs", "service_name", "active_storage_blobs_service_name_not_null"},
    {"deployments", "created_at", "deployments_created_at_not_null1"},
    {"deployments", "id", "deployments_id_not_null1"},
    {"deployments", "status", "deployments_status_not_null1"},
    {"deployments", "updated_at", "deployments_updated_at_not_null1"},
    {"logs", "record_id", "logs_loggable_id_not_null"},
    {"logs", "record_type", "logs_loggable_type_not_null"},
    {"provisionings", "created_at", "deployments_created_at_not_null"},
    {"provisionings", "id", "deployments_id_not_null"},
    {"provisionings", "kind", "deployments_kind_not_null"},
    {"provisionings", "status", "deployments_status_not_null"},
    {"provisionings", "updated_at", "deployments_updated_at_not_null"},
    {"provisionings", "worktree_id", "deployments_worktree_id_not_null"},
    {"variants", "blob_id", "active_storage_variant_records_blob_id_not_null"},
    {"variants", "id", "active_storage_variant_records_id_not_null"},
    {"variants", "variation_digest", "active_storage_variant_records_variation_digest_not_null"}
  ]

  def up do
    execute "DROP TABLE IF EXISTS public.schema_migrations"
    execute "DROP TABLE IF EXISTS public.ar_internal_metadata"

    execute """
    UPDATE public.events e SET content = r.body
    FROM public.action_text_rich_texts r
    WHERE r.record_type = 'Event' AND r.name = 'body' AND r.record_id = e.id
    """

    execute "DROP TABLE public.action_text_rich_texts"

    execute "ALTER TABLE public.logs RENAME COLUMN loggable_type TO record_type"
    execute "ALTER TABLE public.logs RENAME COLUMN loggable_id TO record_id"
    execute "ALTER INDEX public.index_logs_on_loggable RENAME TO index_logs_on_record"

    for {table, column, old} <- @foreign_keys,
        do: rename_constraint(table, old, "#{table}_#{column}_fkey")

    for {table, column, old} <- @not_null,
        do: rename_constraint(table, old, "#{table}_#{column}_not_null")
  end

  def down do
    for {table, column, old} <- @not_null,
        do: rename_constraint(table, "#{table}_#{column}_not_null", old)

    for {table, column, old} <- @foreign_keys,
        do: rename_constraint(table, "#{table}_#{column}_fkey", old)

    execute "ALTER INDEX public.index_logs_on_record RENAME TO index_logs_on_loggable"
    execute "ALTER TABLE public.logs RENAME COLUMN record_id TO loggable_id"
    execute "ALTER TABLE public.logs RENAME COLUMN record_type TO loggable_type"

    execute """
    CREATE TABLE public.action_text_rich_texts (
      id uuid DEFAULT uuidv7() NOT NULL CONSTRAINT action_text_rich_texts_pkey PRIMARY KEY,
      body text,
      created_at timestamp(6) without time zone NOT NULL,
      name character varying NOT NULL,
      record_id uuid NOT NULL,
      record_type character varying NOT NULL,
      updated_at timestamp(6) without time zone NOT NULL
    )
    """

    execute """
    CREATE UNIQUE INDEX index_action_text_rich_texts_uniqueness
    ON public.action_text_rich_texts (record_type, record_id, name)
    """

    execute """
    INSERT INTO public.action_text_rich_texts (body, created_at, name, record_id, record_type, updated_at)
    SELECT content, created_at, 'body', id, 'Event', updated_at FROM public.events WHERE kind = 'note'
    """

    execute "UPDATE public.events SET content = NULL WHERE kind = 'note'"

    execute """
    CREATE TABLE public.ar_internal_metadata (
      key character varying NOT NULL CONSTRAINT ar_internal_metadata_pkey PRIMARY KEY,
      value character varying,
      created_at timestamp(6) without time zone NOT NULL,
      updated_at timestamp(6) without time zone NOT NULL
    )
    """

    execute """
    CREATE TABLE public.schema_migrations (
      version character varying NOT NULL CONSTRAINT schema_migrations_pkey PRIMARY KEY
    )
    """
  end

  defp rename_constraint(table, from, to) do
    execute """
    DO $$ BEGIN
      IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = '#{from}' AND conrelid = to_regclass('public.#{table}')) THEN
        ALTER TABLE public.#{table} RENAME CONSTRAINT #{from} TO #{to};
      END IF;
    END $$
    """
  end
end
