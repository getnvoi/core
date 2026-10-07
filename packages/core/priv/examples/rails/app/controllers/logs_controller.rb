class LogsController < ApplicationController
  def index
    @logs = Log.order(id: :desc)
  end

  # ASKING IS NOT DOING. This enqueues and returns; the row is written by
  # `bin/jobs`, in another process, and reaches every open browser over the
  # cable rather than in this response.
  #
  # It used to happen in `index`, so opening the page recorded an entry — which
  # made the demonstration circular: the list grew whether or not the worker and
  # the socket worked at all.
  #
  # 303, because Turbo requires it on a redirect after a non-GET. A 302 is
  # re-issued with the original method and the browser POSTs to `/`.
  def create
    CreateLogJob.perform_later

    redirect_to root_path, status: :see_other
  end
end
