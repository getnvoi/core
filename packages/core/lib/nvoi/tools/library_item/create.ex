defmodule Nvoi.Tools.LibraryItem.Create do
  @moduledoc """
  Several files in one request, one library item per file, all or none.
  """
  use Nvoi.Tools.Action,
    group: "library_item",
    action: "create",
    category: :library,
    summary: "Upload a document",
    post: "/library",
    status: 201,
    refuses: [400, 503],
    access: :mutation,
    default_role: :drive,
    content: [:multipart]

  param :files, {:list, :file}, required: true

  def call(params, current) do
    files = for %Plug.Upload{} = f <- List.wrap(params["files"]), do: f
    if files == [], do: refuse!("files is required", "parameter_missing", 400)

    {:ok, items} =
      Postgres.transaction(fn ->
        for f <- files,
            do:
              Nvoi.LibraryItem.create!(
                current.workspace,
                f.filename,
                File.read!(f.path),
                f.content_type
              )
      end)

    for item <- items,
        item.read_at == nil,
        do: Oban.insert!(Nvoi.Jobs.WordsJob.new(%{id: item.id}))

    for item <- items,
        Nvoi.Thumbnail.pictures?(item.content_type, item.name),
        do: Nvoi.Jobs.ThumbnailJob.for_item(item)

    Nvoi.Live.signal(current.workspace.id, "library")
    {:created, %{library_items: NvoiWeb.LibraryItemResource.wrap(items)}}
  end
end
