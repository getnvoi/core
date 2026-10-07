defmodule Nvoi.Steps do
  @moduledoc """
  What a machine is made of, run through boxd. `prepare!` converges a plain Ubuntu VM once, in the seed: the user
  `deploy` with sudo and docker, the pinned toolchain (Node, Claude Code, Codex, Playwright's Chromium, mise), the
  converters, our folders, and the filter keeping the agent and the browser off the cloud's metadata address.
  Then the environment's checkout, setup and preview, as `deploy`.
  """
  alias Compute.{Box, Layout}
  alias Nvoi.{Environment, Manifest}

  # How long a preview run waits for the app's port, and how often it looks, in seconds.
  @app_wait 600
  @app_poll 3
  @converters ~w(poppler-utils libreoffice-writer libreoffice-calc libreoffice-impress fonts-liberation tesseract-ocr tesseract-ocr-eng tesseract-ocr-fra)

  def prepare!(box, log) do
    {code, out} = Box.run(box, prepare_script(), wait: 3600)
    log.("prepare", out)

    if code != 0,
      do:
        raise(Compute.Error,
          message:
            "preparing the machine exited #{code}: #{out |> String.split("\n") |> Enum.take(-15) |> Enum.join("\n")}"
        )
  end

  @doc """
  The scratch folder every user writes to (bundles, archives), made again on every start: a box started again comes
  back with a fresh /tmp, and a file put there as root would make the folder root's alone.
  """
  def scratch!(box),
    do: Box.run!(box, "mkdir -p #{Layout.scratch()} && chmod 1777 #{Layout.scratch()}")

  @doc """
  The filter keeping the agent and the browser off the cloud's metadata address, installed in the seed and again on
  every start: a fork or a wake does not always bring the saved ruleset back. `Nvoi.Fence` checks it.
  """
  def fence!(box), do: Box.run!(box, fence_script())

  defp fence_script do
    user = Layout.user()

    """
    uids=$(for u in #{user} #{Layout.browser_user()}; do id -u "$u"; done | paste -sd, -)
    nft -f - <<EOF
    table inet nvoi
    delete table inet nvoi
    table inet nvoi {
      chain output {
        type filter hook output priority filter; policy accept;
        meta skuid { $uids } ip daddr 169.254.0.0/16 udp dport 53 accept
        meta skuid { $uids } ip daddr 169.254.0.0/16 tcp dport 53 accept
        meta skuid { $uids } ip daddr 169.254.0.0/16 reject
        meta skuid { $uids } ip6 daddr fe80::/10 udp dport 53 accept
        meta skuid { $uids } ip6 daddr fe80::/10 tcp dport 53 accept
        meta skuid { $uids } ip6 daddr fe80::/10 reject
      }
    }
    EOF
    nft list ruleset > /etc/nftables.conf && systemctl enable -q nftables
    """
  end

  defp prepare_script do
    user = Layout.user()
    node = Layout.node_version()
    home = "#{Layout.toolchain()}/node-v#{node}"
    sha = Layout.node_sha256()

    packages =
      "@anthropic-ai/claude-code@#{Layout.claude_version()} @openai/codex@#{Layout.codex_version()} playwright-core@#{Layout.playwright_version()}"

    """
    set -e
    export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a
    command -v cloud-init >/dev/null && { cloud-init status --wait >/dev/null || [ $? -eq 2 ]; }
    grep -q " $(hostname)$" /etc/hosts || echo "127.0.1.1 $(hostname)" >> /etc/hosts
    printf 'debconf debconf/frontend select Noninteractive\\n' | debconf-set-selections
    mkdir -p /etc/needrestart/conf.d && printf "\\$nrconf{restart} = 'a';\\n\\$nrconf{kernelhints} = 0;\\n" > /etc/needrestart/conf.d/nvoi.conf
    for c in sudo curl git gpg cc make pkg-config pg_config psql xz nft; do command -v $c >/dev/null || missing=1; done
    if [ -n "$missing" ]; then
      apt-get update -qq
      apt-get install -y -qq sudo curl ca-certificates gnupg git build-essential pkg-config libssl-dev libyaml-dev zlib1g-dev libpq-dev postgresql-client tzdata xz-utils nftables >/dev/null
    fi
    id -u #{user} >/dev/null 2>&1 || useradd -m -s /bin/bash -G sudo #{user}
    [ -f /etc/sudoers.d/#{user} ] || echo '#{user} ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/#{user}
    mkdir -p #{Layout.scratch()} && chmod 1777 #{Layout.scratch()}

    if ! command -v docker >/dev/null; then
      install -m 0755 -d /etc/apt/keyrings
      curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
      echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu noble stable" > /etc/apt/sources.list.d/docker.list
      apt-get update -qq && apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin >/dev/null
    fi
    usermod -aG docker #{user}
    systemctl enable -q --now docker
    for i in $(seq 1 60); do docker info >/dev/null 2>&1 && break; sleep 2; done

    if [ "$(#{Layout.node()} -v 2>/dev/null)" != "v#{node}" ]; then
      case "$(uname -m)" in x86_64) arch=x64; sha=#{sha["x64"]};; aarch64|arm64) arch=arm64; sha=#{sha["arm64"]};; *) echo "no Node for $(uname -m)" >&2; exit 1;; esac
      tmp="$(mktemp -d)"
      curl -fsSL "https://nodejs.org/dist/v#{node}/node-v#{node}-linux-$arch.tar.xz" -o "$tmp/node.tar.xz"
      echo "$sha  $tmp/node.tar.xz" | sha256sum -c --status
      rm -rf #{home} && mkdir -p #{home} && tar -xJf "$tmp/node.tar.xz" -C #{home} --strip-components=1 && rm -rf "$tmp"
      ln -sfn #{home} #{Layout.node_home()}
    fi
    PATH=#{Layout.node_home()}/bin:$PATH npm install -g --no-audit --no-fund --silent #{packages}
    command -v mise >/dev/null || (curl -fsSL https://mise.run | MISE_INSTALL_PATH=/usr/local/bin/mise sh >/dev/null)
    printf '%s\\n' 'export PATH="$HOME/.local/share/mise/shims:#{Layout.node_home()}/bin:$PATH"' > /etc/profile.d/nvoi.sh
    sudo -u #{user} git config --global user.name nvoi && sudo -u #{user} git config --global user.email nvoi@local

    mkdir -p #{Enum.join(Layout.owned(), " ")} && chown #{user}:#{user} #{Enum.join(Layout.owned(), " ")}
    id -u #{Layout.browser_user()} >/dev/null 2>&1 || useradd --system --home-dir #{Layout.captures()} --shell /usr/sbin/nologin #{Layout.browser_user()}
    mkdir -p #{Layout.captures()} #{Layout.videos()} && chown #{Layout.browser_user()}:#{Layout.browser_user()} #{Layout.captures()} #{Layout.videos()}
    PLAYWRIGHT_BROWSERS_PATH=#{Layout.playwright()} #{Layout.node()} #{Layout.node_modules()}/playwright-core/cli.js install --with-deps chromium >/dev/null
    chown -R #{user}:#{user} #{Layout.playwright()}
    command -v pdftotext >/dev/null && command -v soffice >/dev/null && command -v tesseract >/dev/null || apt-get install -y -qq --no-install-recommends #{Enum.join(@converters, " ")} fonts-noto-cjk ffmpeg >/dev/null
    mkdir -p #{Layout.renders()} && chown #{Layout.browser_user()}:#{user} #{Layout.renders()} && chmod 2770 #{Layout.renders()}

    #{fence_script()}
    echo prepared
    """
  end

  @doc "The environment's recipe: the repository's nvoi.yml, else the commands typed, else the matched runtime."
  def recipe(box, environment) do
    cond do
      environment.manifest == Manifest.file_name() ->
        case Box.read(box, "#{Layout.workspace()}/#{Manifest.file_name()}") do
          {:ok, text} ->
            Manifest.parse(text, Manifest.file_name())

          :missing ->
            raise Manifest.Invalid, message: "#{Manifest.file_name()} is missing at this commit"
        end

      environment.manifest && not Environment.typed?(environment) ->
        Manifest.runtime(environment.manifest)

      true ->
        Manifest.typed(environment)
    end
  end

  @doc "The environment's digest, with the manifest's own when the commands come from it."
  def digest(environment, %{typed: true, tools: []}), do: Environment.setup_digest(environment)

  def digest(environment, %{typed: true, tools: tools}),
    do:
      :crypto.hash(:sha256, "#{Environment.setup_digest(environment)}/#{Enum.join(tools, "\n")}")
      |> Base.encode16(case: :lower)
      |> binary_part(0, 16)

  def digest(environment, manifest),
    do:
      :crypto.hash(:sha256, "#{Environment.setup_digest(environment)}/#{manifest.digest}")
      |> Base.encode16(case: :lower)
      |> binary_part(0, 16)

  defp as_user(opts), do: Keyword.merge([as: Layout.user(), dir: Layout.workspace()], opts)

  # How many of a failed setup command's last lines its failure carries.
  @failure_lines 15

  defp last_lines(out),
    do:
      out
      |> String.trim_trailing()
      |> String.split("\n")
      |> Enum.take(-@failure_lines)
      |> Enum.join("\n")

  @doc "Runs each setup command as `deploy`, then marks this digest at HEAD so a fork at the same commit skips it."
  def setup!(box, environment, env, log) do
    manifest = recipe(box, environment)

    if manifest.ignore != [],
      do:
        Box.run!(
          box,
          "printf '%s\\n' #{Enum.map_join(manifest.ignore, " ", &"'#{&1}'")} >> .git/info/exclude",
          as_user([])
        )

    # The first command that fails stops setup: the ones after it depend on it. Its last lines say why.
    failure =
      Enum.find_value(Manifest.setup_commands(manifest), fn c ->
        {code, out} = Box.run(box, c.command, as_user(env: env, wait: 1800))
        log.(c.name, out)
        if code != 0, do: "#{c.name} exited #{code}: #{last_lines(out)}"
      end)

    if failure, do: raise(Compute.Error, message: "setup failed: #{failure}")

    Box.run!(
      box,
      "mkdir -p #{Layout.state()} && echo #{digest(environment, manifest)} $(git rev-parse HEAD) > #{Layout.state()}/setup.done",
      as_user([])
    )
  end

  def setup_done?(box, environment) do
    manifest = recipe(box, environment)

    {_, out} =
      Box.run(
        box,
        "cat #{Layout.state()}/setup.done 2>/dev/null; echo; git rev-parse HEAD",
        as_user([])
      )

    [done, _, head | _] = String.split(out <> "\n\n", "\n")
    String.trim(done) == "#{digest(environment, manifest)} #{String.trim(head)}"
  end

  @doc "Starts each preview service as a boxd command named for it, boxd watching its port; waits for the app's."
  def preview!(box, environment, env) do
    manifest = recipe(box, environment)
    commands = Manifest.service_commands(manifest)

    if commands == [],
      do: raise(ArgumentError, "environment #{environment.name} has no preview commands")

    running = for c <- Box.commands(box), into: %{}, do: {c["name"], c["id"]}
    port = Manifest.port(manifest)

    for c <- commands, id = running["preview:#{c.name}"], do: Box.kill(box, id)
    free!(box, Enum.uniq(Enum.reject([port | Enum.map(manifest.services, & &1.port)], &is_nil/1)))

    started =
      for c <- commands do
        service = Enum.find(manifest.services, &(&1.name == c.name))

        answer =
          Box.start(
            box,
            ["bash", "-lc", c.command],
            as_user(name: "preview:#{c.name}", port: service.port, env: env)
          )

        %{name: c.name, id: answer["id"], port: service.port}
      end

    # The service that listens on the app's port; every one when none says so.
    watched =
      case Enum.filter(started, &(&1.port == port)) do
        [] -> started
        app -> app
      end

    await!(box, port, watched, div(@app_wait, @app_poll))
  end

  # Stops whatever still listens on the app's ports: a process boxd no longer holds (one a fork brought from its seed,
  # or one that outlived a restart of boxd) cannot be killed through it, and would keep serving the old app.
  defp free!(box, ports) do
    script =
      for port <- ports, into: "" do
        hex = port |> Integer.to_string(16) |> String.pad_leading(4, "0")

        """
        for inode in $(awk '$2 ~ /:#{hex}$/ && $4 == "0A" {print $10}' /proc/net/tcp /proc/net/tcp6 2>/dev/null); do
          for fd in /proc/[0-9]*/fd/*; do
            if [ "$(readlink "$fd" 2>/dev/null)" = "socket:[$inode]" ]; then
              pid=${fd#/proc/}; pid=${pid%%/*}
              group=$(ps -o pgid= -p "$pid" | tr -d ' ')
              kill -KILL -- "-$group" 2>/dev/null || kill -KILL "$pid" 2>/dev/null
            fi
          done
        done
        """
      end

    Box.run(box, script <> "sleep 1; true", wait: 60)
  end

  # Until the app's port answers, then one request so its first page is compiled. A watched service that ended fails
  # the run at once with its last lines, rather than after the whole wait.
  defp await!(box, port, watched, tries) do
    case Box.run(box, "(echo > /dev/tcp/127.0.0.1/#{port}) 2>/dev/null", wait: 30) do
      {0, _} ->
        Box.run(box, "curl -s -o /dev/null --max-time 60 http://127.0.0.1:#{port}/ || true",
          wait: 90
        )

        port

      _ ->
        ended =
          Enum.find_value(watched, fn s ->
            status = Box.status(box, s.id)
            if status["exited"], do: {s, status}
          end)

        cond do
          ended ->
            {service, status} = ended

            said =
              box
              |> Box.logs(service.id)
              |> to_string()
              |> String.split("\n", trim: true)
              |> Enum.take(-20)
              |> Enum.join("\n")

            code = if status["coded"] == false, do: "", else: " (exit #{status["exit_code"]})"

            raise Compute.Error,
              message:
                "#{service.name} ended before the app answered on port #{port}#{code}: #{said}"

          tries <= 1 ->
            raise Compute.Error, message: "the app did not answer on port #{port}"

          true ->
            Process.sleep(@app_poll * 1000)
            await!(box, port, watched, tries - 1)
        end
    end
  end
end
