defmodule NvoiWeb.ModeTest do
  # The sign-in and whoami say the server's mode; the core alone is selfhosted, and its document marks nothing.
  use NvoiWeb.ConnCase, async: false

  test "the sign-in and whoami answer the core's mode, selfhosted" do
    email = "mode#{System.unique_integer([:positive])}@etudes-studio.com"
    body = %{email: email, password: "a long password", workspace: "mode"}
    build_conn() |> post("/api/v1/signup", body) |> json_response(201)
    Nvoi.User.approve!(email)

    signed = build_conn() |> post("/api/v1/login", body) |> json_response(201)
    assert signed["mode"] == "selfhosted"

    whoami =
      build_conn()
      |> put_req_header("authorization", "Bearer " <> signed["token"])
      |> get("/api/v1/whoami")
      |> json_response(200)

    assert whoami["mode"] == "selfhosted"
  end

  test "the core's document marks no operation with a mode" do
    refute Nvoi.Tools.Document.build() |> Jason.encode!() |> String.contains?("x-nvoi-mode")
  end
end
