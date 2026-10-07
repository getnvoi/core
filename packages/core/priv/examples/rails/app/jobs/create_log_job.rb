# WHAT THE WORKER DOES, and it is the whole demonstration: a row written in a
# process that never served a request, announced to every browser that is open.
#
# `bin/jobs` runs this — a separate process from `bin/rails server`, which is why
# `config/cable.yml` uses solid_cable rather than the `async` adapter: an
# in-process broadcast from here would reach nobody.
class CreateLogJob < ApplicationJob
  queue_as :default

  def perform
    log = Log.create!

    Turbo::StreamsChannel.broadcast_prepend_to(
      "logs",
      target: "logs",
      partial: "logs/log",
      locals: { log: log }
    )
  end
end
