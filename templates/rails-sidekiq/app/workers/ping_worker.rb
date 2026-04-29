class PingWorker
  include Sidekiq::Worker

  def perform(ts)
    Rails.logger.info "[PingWorker] received ts=#{ts} now=#{Time.now.to_i}"
  end
end
