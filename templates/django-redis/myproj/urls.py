from django.urls import path
from myproj import views

urlpatterns = [
    path("", views.root),
    path("health", views.health),
    path("cache-check", views.cache_check),
]
