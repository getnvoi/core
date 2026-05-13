#!/usr/bin/env bash
# Probes https://ha.nvoi.to/ twice a second and prints HTTP code +
# total time, green on 200 / red on anything else.
#
# Run it in a separate terminal during a redeploy to prove the
# substrate's no-downtime claim: scaling masters up/down, adding or
# removing workers, re-pinning workloads — the stream should stay
# solid green, with at most a brief blip during pod rollouts. Any
# sustained non-200 window is a regression worth investigating.

while :; do
  ts=$(date +%H:%M:%S)
  out=$(curl -s -o /dev/null -w '%{http_code} %{time_total}s' --max-time 3 https://ha.nvoi.to/)
  code=${out%% *}
  if [ "$code" = "200" ]; then c=32; else c=31; fi
  printf '\033[%sm%s  %s\033[0m\n' "$c" "$ts" "$out"
  sleep 0.5
done