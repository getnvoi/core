defmodule Nvoi.ScheduleTest do
  # The schedule is named entries an edition adds to; NVOI_CRON picks some or none.
  use ExUnit.Case, async: false

  setup do
    schedule = Application.get_env(:nvoi, :schedule)

    Application.put_env(:nvoi, :schedule,
      idle: {"* * * * *", IdleJob},
      tasks: {"0 * * * *", TasksJob}
    )

    on_exit(fn -> Application.put_env(:nvoi, :schedule, schedule) end)
  end

  test "every entry by default, none with 0, the named ones else" do
    assert Nvoi.Schedule.crontab(nil) == [{"* * * * *", IdleJob}, {"0 * * * *", TasksJob}]
    assert Nvoi.Schedule.crontab("") == Nvoi.Schedule.crontab(nil)
    assert Nvoi.Schedule.crontab("0") == []
    assert Nvoi.Schedule.crontab("tasks, nope") == [{"0 * * * *", TasksJob}]
  end

  test "an edition's entries merge with the core's by name" do
    Application.put_env(
      :nvoi,
      :schedule,
      Keyword.merge(Application.get_env(:nvoi, :schedule), extra: {"5 * * * *", X})
    )

    assert {"5 * * * *", X} in Nvoi.Schedule.crontab(nil)
  end
end
