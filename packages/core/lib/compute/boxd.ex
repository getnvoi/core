defmodule Compute.Boxd do
  @moduledoc "boxd as a systemd unit: started at boot, restarted when it dies, reading its token from /etc/nvoi/env."
  def unit do
    """
    [Unit]
    Description=nvoi box agent
    After=network-online.target
    ConditionPathExists=/etc/nvoi/env

    [Service]
    EnvironmentFile=/etc/nvoi/env
    Environment=HOME=/root
    Environment=NVOI_LOG_DIR=/var/lib/nvoi/logs
    Environment=NVOI_WORKSPACE=#{Compute.Layout.workspace()}
    Environment=NVOI_LISTEN=:#{Compute.Box.port()}
    Environment=NVOI_TERMINAL_USER=#{Compute.Layout.user()}
    ExecStart=/usr/local/bin/boxd
    KillMode=process
    Restart=always
    RestartSec=2

    [Install]
    WantedBy=multi-user.target
    """
  end
end
