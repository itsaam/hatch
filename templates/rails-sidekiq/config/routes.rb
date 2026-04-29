Rails.application.routes.draw do
  root to: "app#root"
  get "/health", to: "app#health"
  get "/db-check", to: "app#db_check"
  get "/cache-check", to: "app#cache_check"
  get "/enqueue", to: "app#enqueue"
end
