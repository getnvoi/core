defmodule Nvoi.SecretsTest do
  use ExUnit.Case, async: true

  test "masks every value, longest first" do
    assert Nvoi.Secrets.mask("key sk-abcdefgh-12345 and sk-abcdefgh", [
             "sk-abcdefgh-12345",
             "sk-abcdefgh"
           ]) ==
             "key [secret] and [secret]"

    assert Nvoi.Secrets.mask(nil, ["anything"]) == nil
  end

  test "a Codex auth file is masked whole and token by token" do
    file =
      Jason.encode!(%{
        "tokens" => %{"access_token" => "eyJ-access-123456", "refresh_token" => "rt-987654321"}
      })

    values = Llm.Codex.secret_values(%{"auth" => file})
    assert file in values and "eyJ-access-123456" in values and "rt-987654321" in values
  end
end
