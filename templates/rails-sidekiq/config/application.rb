require_relative "boot"

require "rails"
require "active_model/railtie"
require "active_job/railtie"
require "active_record/railtie"
require "action_controller/railtie"

Bundler.require(*Rails.groups)

module HatchRailsSidekiq
  class Application < Rails::Application
    config.load_defaults 7.1
    config.api_only = true
    config.active_job.queue_adapter = :sidekiq
    config.eager_load = true
    config.hosts.clear
  end
end
