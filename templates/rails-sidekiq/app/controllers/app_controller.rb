class AppController < ActionController::API
  def root
    render json: { status: "ok", service: "rails-sidekiq" }
  end

  def health
    render json: { status: "ok" }
  end

  def db_check
    one = ActiveRecord::Base.connection.select_value("SELECT 1").to_i
    count = ActiveRecord::Base.connection.select_value("SELECT count(*) FROM preview_seed").to_i
    render json: { db: "ok", select_1: one, preview_seed_rows: count }
  rescue => e
    render json: { db: "error", error: e.message }, status: 500
  end

  def cache_check
    Sidekiq.redis do |conn|
      conn.set("hatch:preview:value", "set-at-#{Time.now.to_i}", ex: 300, nx: true)
      value = conn.get("hatch:preview:value")
      hits = conn.incr("hatch:preview:hits")
      conn.expire("hatch:preview:hits", 300)
      render json: { cache: "ok", value: value, hits: hits.to_i }
    end
  rescue => e
    render json: { cache: "error", error: e.message }, status: 500
  end

  def enqueue
    jid = PingWorker.perform_async(Time.now.to_i)
    render json: { enqueued: true, jid: jid }
  end
end
