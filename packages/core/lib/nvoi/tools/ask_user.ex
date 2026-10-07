defmodule Nvoi.Tools.AskUser do
  @moduledoc "Questions a person answers; the call waits on them at every level but deny."
  use Nvoi.Tools.Base,
    name: "ask_user",
    category: :session,
    icon: "message-circle-question",
    label: "Question",
    done: "Answer sent",
    asks: true

  def description(_) do
    "Ask the user one to four questions, each with two to four options, when a choice is theirs to make: scope, a trade-off, " <>
      "which of several ways. The user may pick several options where multiSelect is true and may always write their own " <>
      "answer instead, so never add an \"Other\" option. Their answers come back as your next message; end your turn after " <>
      "asking. Do not ask what you can find out yourself."
  end

  def schema do
    option = %{
      type: "object",
      required: ~w(label description),
      properties: %{
        label: %{type: "string", description: "One to five words"},
        description: %{type: "string", description: "What choosing it means"}
      }
    }

    question = %{
      type: "object",
      required: ~w(question header options multiSelect),
      properties: %{
        question: %{type: "string", description: "The whole question, ending with ?"},
        header: %{type: "string", description: "A short label, at most 12 characters"},
        options: %{type: "array", items: option, minItems: 2, maxItems: 4},
        multiSelect: %{type: "boolean"}
      }
    }

    %{
      type: "object",
      required: ["questions"],
      properties: %{questions: %{type: "array", items: question, minItems: 1, maxItems: 4}}
    }
  end

  def questions(input) do
    for q <- List.wrap(input["questions"]), is_map(q), not Nvoi.Text.blank?(q["question"]) do
      %{
        question: to_string(q["question"]),
        header: to_string(q["header"]),
        multiple: q["multiSelect"] == true,
        options:
          for(
            o <- List.wrap(q["options"]),
            is_map(o),
            not Nvoi.Text.blank?(o["label"]),
            do: %{label: to_string(o["label"]), description: to_string(o["description"])}
          )
      }
    end
  end

  @doc "The input with the person's answers: the options picked, then what they wrote."
  def answer(input, picks, notes) do
    answers =
      input
      |> questions()
      |> Enum.with_index()
      |> Enum.map(fn {q, i} ->
        picked =
          (picks || %{})
          |> Map.get(to_string(i))
          |> List.wrap()
          |> Enum.map(&to_string/1)
          |> Enum.reject(&(&1 == ""))

        picked = if q.multiple, do: picked, else: Enum.take(picked, 1)
        unknown = picked -- Enum.map(q.options, & &1.label)

        if unknown != [],
          do: raise(Nvoi.Tools.Error, message: "#{inspect(unknown)} is not an option")

        written = (notes || %{}) |> Map.get(to_string(i)) |> to_string() |> String.trim()
        answer = picked ++ if(written == "", do: [], else: [written])

        if answer == [],
          do:
            raise(Nvoi.Tools.Error,
              message: "no answer",
              plain: "Answer #{Nvoi.Text.presence(q.header) || q.question} first"
            )

        answer
      end)

    Map.put(input, "answers", answers)
  end

  def call(input, _context) do
    answers = List.wrap(input["answers"])
    if answers == [], do: raise(Nvoi.Tools.Error, message: "no answers")

    lines =
      input
      |> questions()
      |> Enum.zip(answers)
      |> Enum.map(fn {q, a} -> "- #{q.question} #{a |> List.wrap() |> Enum.join("; ")}" end)

    "the user answered:\n" <> Enum.join(lines, "\n")
  end
end
