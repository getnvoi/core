defmodule Nvoi.Files do
  @moduledoc """
  The checkout's files as they are on the box, for the session's Files view. The box holds the only copy, so both
  the listing and a file are read there while the machine runs. A file is shown only when it is text and small
  enough to draw; anything else comes back refused, with the reason.
  """
  alias Compute.{Box, Layout}

  # Tracked files and untracked ones git does not ignore; more than this are cut, and the listing says so.
  @cap 5_000
  # Past this a file is not sent to the browser: highlighting it would stall the page.
  @limit 1_000_000

  defmodule Error, do: defexception([:message])

  @doc "`%{paths, total, cut}`."
  def list(box) do
    lines =
      run(box, """
      set -o pipefail
      git -c core.quotePath=false ls-files --cached --others --exclude-standard \\
        | awk -v cap=#{@cap} 'NR <= cap { print } END { print "__total " NR }'
      """)

    {total, paths} = List.pop_at(lines, -1)

    case Regex.run(~r/\A__total (\d+)\z/, to_string(total)) do
      [_, n] ->
        %{
          paths: Enum.uniq(paths),
          total: String.to_integer(n),
          cut: String.to_integer(n) > length(Enum.uniq(paths))
        }

      nil ->
        raise Error, message: "git ls-files said nothing"
    end
  end

  @doc """
  `%{path, size, text, refused, message}`; `refused` is nil for a file shown, else missing, binary or large. The
  path is relative to the checkout and stays inside it, links resolved; .git is not the checkout's. The markers
  come first, then the text, so a line of the file that looks like one is past them.
  """
  def read(box, path) do
    case clean(path) do
      nil ->
        blob(to_string(path), 0, nil, "missing")

      clean ->
        lines =
          run(box, """
          f=#{shell(clean)}
          root=$(realpath .)
          real=$(realpath -e -- "$f" 2>/dev/null) || { echo __missing; exit 0; }
          case "$real" in "$root"/*) ;; *) echo __missing; exit 0 ;; esac
          [ -f "$real" ] || { echo __missing; exit 0; }
          size=$(wc -c < "$real" | tr -d ' ')
          echo "__size $size"
          [ "$size" -gt #{@limit} ] && { echo __large; exit 0; }
          [ "$size" -eq 0 ] || grep -Iq . -- "$real" || { echo __binary; exit 0; }
          echo __text
          cat -- "$real" 2>/dev/null
          """)

        head = Enum.take(lines, 3)

        size =
          Enum.find_value(
            head,
            0,
            &with("__size " <> n <- &1, do: String.to_integer(n), else: (_ -> nil))
          )

        case Enum.find_index(head, &(&1 == "__text")) do
          nil ->
            refused =
              Enum.find_value(
                head,
                "missing",
                &(Regex.run(~r/\A__(missing|binary|large)\z/, &1)
                  |> then(fn m -> m && List.last(m) end))
              )

            blob(clean, size, nil, refused)

          start ->
            blob(clean, size, lines |> Enum.drop(start + 1) |> Enum.join("\n"), nil)
        end
    end
  end

  @doc "A relative path with no way out of the checkout, or nil."
  def clean(path) do
    path = to_string(path)
    parts = String.split(path, "/")

    if path == "" or String.starts_with?(path, "/") or String.contains?(path, <<0>>) or
         ".." in parts or
         hd(parts) == ".git",
       do: nil,
       else: path
  end

  defp blob(path, size, text, refused),
    do: %{
      path: path,
      size: size,
      text: text,
      refused: refused,
      message: message(path, size, refused)
    }

  defp message(_, _, nil), do: nil
  defp message(path, _, "missing"), do: "#{path} is not in the checkout."
  defp message(path, _, "binary"), do: "#{path} is a binary file, so it is not shown."

  defp message(path, size, "large"),
    do: "#{path} is #{Float.round(size / 1_048_576, 1)} MB, over the 1 MB shown here."

  defp shell(text), do: "'" <> String.replace(text, "'", "'\\''") <> "'"

  # Every line the script printed, errors included, so a failure can say what went wrong.
  defp run(box, script) do
    case Box.run(box, script, as: Layout.user(), dir: Layout.workspace(), wait: 60) do
      {0, output} ->
        String.split(output, "\n") |> drop_last_empty()

      {code, output} ->
        raise Error,
          message:
            "exit #{code}: #{output |> String.split("\n") |> Enum.take(-5) |> Enum.join(" ") |> String.slice(0, 300)}"
    end
  rescue
    e in Compute.Error -> raise Error, message: Exception.message(e)
  end

  defp drop_last_empty(lines),
    do: if(List.last(lines) == "", do: Enum.drop(lines, -1), else: lines)
end
