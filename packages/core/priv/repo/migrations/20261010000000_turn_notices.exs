defmodule Nvoi.Postgres.Migrations.TurnNotices do
  @moduledoc """
  The one source of a turn's changes: every write to a row the conversation draws of a turn (the turn, its events,
  captures, saved artifact versions and their artifact's name, attached files and their thumbnails) sends
  `pg_notify('turn', '<session id>:<turn id>')` when it commits, and the turn it was folded into is named with it.
  `Nvoi.Live.Listener` hears it and tells the apps. A new table the conversation reads gets a trigger here.
  """
  use Ecto.Migration

  def up do
    execute """
    CREATE FUNCTION public.turn_notice(turn uuid) RETURNS void LANGUAGE plpgsql AS $$
    DECLARE t record;
    BEGIN
      SELECT id, agent_session_id, lead_id INTO t FROM public.turns WHERE id = turn;
      IF FOUND THEN
        PERFORM pg_notify('turn', t.agent_session_id::text || ':' || t.id::text);
        IF t.lead_id IS NOT NULL THEN
          PERFORM pg_notify('turn', t.agent_session_id::text || ':' || t.lead_id::text);
        END IF;
      END IF;
    END $$
    """

    # A turn's own row: on its delete it is gone, so OLD names it.
    execute """
    CREATE FUNCTION public.turns_noticed() RETURNS trigger LANGUAGE plpgsql AS $$
    DECLARE r record;
    BEGIN
      IF TG_OP = 'DELETE' THEN r := OLD; ELSE r := NEW; END IF;
      PERFORM pg_notify('turn', r.agent_session_id::text || ':' || r.id::text);
      IF r.lead_id IS NOT NULL THEN
        PERFORM pg_notify('turn', r.agent_session_id::text || ':' || r.lead_id::text);
      END IF;
      RETURN NULL;
    END $$
    """

    # Rows that carry their turn: events, captures, artifact versions.
    execute """
    CREATE FUNCTION public.turn_rows_noticed() RETURNS trigger LANGUAGE plpgsql AS $$
    DECLARE r record;
    BEGIN
      IF TG_OP = 'DELETE' THEN r := OLD; ELSE r := NEW; END IF;
      IF r.turn_id IS NOT NULL THEN PERFORM public.turn_notice(r.turn_id); END IF;
      RETURN NULL;
    END $$
    """

    # A file attached to a turn, an event (a screenshot) or a capture.
    execute """
    CREATE FUNCTION public.attachments_noticed() RETURNS trigger LANGUAGE plpgsql AS $$
    DECLARE r record; turn uuid;
    BEGIN
      IF TG_OP = 'DELETE' THEN r := OLD; ELSE r := NEW; END IF;
      turn := CASE r.record_type
        WHEN 'Turn' THEN r.record_id
        WHEN 'Event' THEN (SELECT turn_id FROM public.events WHERE id = r.record_id)
        WHEN 'Capture' THEN (SELECT turn_id FROM public.captures WHERE id = r.record_id)
      END;
      IF turn IS NOT NULL THEN PERFORM public.turn_notice(turn); END IF;
      RETURN NULL;
    END $$
    """

    # An artifact renamed: the turns that saved a version of it show its name.
    execute """
    CREATE FUNCTION public.artifacts_noticed() RETURNS trigger LANGUAGE plpgsql AS $$
    DECLARE turn uuid;
    BEGIN
      FOR turn IN SELECT DISTINCT turn_id FROM public.artifact_versions
                  WHERE artifact_id = NEW.id AND turn_id IS NOT NULL LOOP
        PERFORM public.turn_notice(turn);
      END LOOP;
      RETURN NULL;
    END $$
    """

    # A thumbnail rendered: the turns holding a file of those bytes (its SHA-256 is in the blob's metadata).
    execute """
    CREATE FUNCTION public.thumbnails_noticed() RETURNS trigger LANGUAGE plpgsql AS $$
    DECLARE turn uuid;
    BEGIN
      FOR turn IN SELECT DISTINCT a.record_id FROM public.active_storage_attachments a
                  JOIN public.active_storage_blobs b ON b.id = a.blob_id
                  WHERE a.record_type = 'Turn' AND b.metadata LIKE '%' || NEW.digest || '%' LOOP
        PERFORM public.turn_notice(turn);
      END LOOP;
      RETURN NULL;
    END $$
    """

    # The columns a turn shows; log_offset and updated_at move on every line and are left out.
    execute """
    CREATE TRIGGER turns_noticed AFTER INSERT OR DELETE OR UPDATE OF
      status, prompt, lead_id, cents, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
      conversation, note_id, user_id, internal
    ON public.turns FOR EACH ROW EXECUTE FUNCTION public.turns_noticed()
    """

    for table <- ~w(events captures artifact_versions) do
      execute """
      CREATE TRIGGER #{table}_noticed AFTER INSERT OR UPDATE OR DELETE ON public.#{table}
      FOR EACH ROW EXECUTE FUNCTION public.turn_rows_noticed()
      """
    end

    execute """
    CREATE TRIGGER attachments_noticed AFTER INSERT OR UPDATE OR DELETE ON public.active_storage_attachments
    FOR EACH ROW EXECUTE FUNCTION public.attachments_noticed()
    """

    execute """
    CREATE TRIGGER artifacts_noticed AFTER UPDATE OF name, kind ON public.artifacts
    FOR EACH ROW EXECUTE FUNCTION public.artifacts_noticed()
    """

    execute """
    CREATE TRIGGER thumbnails_noticed AFTER INSERT OR UPDATE OF state ON public.thumbnails
    FOR EACH ROW EXECUTE FUNCTION public.thumbnails_noticed()
    """
  end

  def down do
    for {trigger, table} <- [
          turns_noticed: :turns,
          events_noticed: :events,
          captures_noticed: :captures,
          artifact_versions_noticed: :artifact_versions,
          attachments_noticed: :active_storage_attachments,
          artifacts_noticed: :artifacts,
          thumbnails_noticed: :thumbnails
        ],
        do: execute("DROP TRIGGER #{trigger} ON public.#{table}")

    for function <-
          ~w(turns_noticed turn_rows_noticed attachments_noticed artifacts_noticed thumbnails_noticed),
        do: execute("DROP FUNCTION public.#{function}()")

    execute "DROP FUNCTION public.turn_notice(uuid)"
  end
end
