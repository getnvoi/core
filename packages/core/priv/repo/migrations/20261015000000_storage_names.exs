defmodule Nvoi.Postgres.Migrations.StorageNames do
  @moduledoc """
  Stored files under the server's own names: `active_storage_blobs`, `_attachments` and `_variant_records` become
  `blobs`, `attachments` and `variants`, with their keys, indexes and foreign keys. Renames only, so every row
  stays. A variant's image was attached under the record type `ActiveStorage::VariantRecord`; it is `Variant` now.
  The thumbnails' trigger function names the tables in its body, so it is made again over the new names; the
  attachments' trigger follows its table.
  """
  use Ecto.Migration

  @tables [
    {"active_storage_blobs", "blobs"},
    {"active_storage_attachments", "attachments"},
    {"active_storage_variant_records", "variants"}
  ]

  @indexes [
    {"index_active_storage_blobs_on_key", "blobs_key_index"},
    {"index_active_storage_attachments_on_blob_id", "attachments_blob_id_index"},
    {"index_active_storage_attachments_uniqueness",
     "attachments_record_type_record_id_name_blob_id_index"},
    {"index_active_storage_variant_records_uniqueness", "variants_blob_id_variation_digest_index"}
  ]

  @foreign_keys [
    {"attachments", "fk_rails_c3b3935057", "attachments_blob_id_fkey"},
    {"variants", "fk_rails_993965df05", "variants_blob_id_fkey"}
  ]

  def up do
    for {old, new} <- @tables do
      execute "ALTER TABLE public.#{old} RENAME TO #{new}"
      execute "ALTER TABLE public.#{new} RENAME CONSTRAINT #{old}_pkey TO #{new}_pkey"
    end

    for {old, new} <- @indexes, do: execute("ALTER INDEX public.#{old} RENAME TO #{new}")

    for {table, old, new} <- @foreign_keys,
        do: execute("ALTER TABLE public.#{table} RENAME CONSTRAINT #{old} TO #{new}")

    execute "UPDATE public.attachments SET record_type = 'Variant' WHERE record_type = 'ActiveStorage::VariantRecord'"
    thumbnails_noticed("attachments", "blobs")
  end

  def down do
    thumbnails_noticed("active_storage_attachments", "active_storage_blobs")

    execute "UPDATE public.attachments SET record_type = 'ActiveStorage::VariantRecord' WHERE record_type = 'Variant'"

    for {table, old, new} <- @foreign_keys,
        do: execute("ALTER TABLE public.#{table} RENAME CONSTRAINT #{new} TO #{old}")

    for {old, new} <- @indexes, do: execute("ALTER INDEX public.#{new} RENAME TO #{old}")

    for {old, new} <- @tables do
      execute "ALTER TABLE public.#{new} RENAME CONSTRAINT #{new}_pkey TO #{old}_pkey"
      execute "ALTER TABLE public.#{new} RENAME TO #{old}"
    end
  end

  # A thumbnail rendered: the turns holding a file of those bytes (its SHA-256 is in the blob's metadata).
  defp thumbnails_noticed(attachments, blobs) do
    execute """
    CREATE OR REPLACE FUNCTION public.thumbnails_noticed() RETURNS trigger LANGUAGE plpgsql AS $$
    DECLARE turn uuid;
    BEGIN
      FOR turn IN SELECT DISTINCT a.record_id FROM public.#{attachments} a
                  JOIN public.#{blobs} b ON b.id = a.blob_id
                  WHERE a.record_type = 'Turn' AND b.metadata LIKE '%' || NEW.digest || '%' LOOP
        PERFORM public.turn_notice(turn);
      END LOOP;
      RETURN NULL;
    END $$
    """
  end
end
