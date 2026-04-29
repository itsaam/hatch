import time
from django.core.cache import cache
from django.http import JsonResponse


def root(request):
    return JsonResponse({"status": "ok", "service": "django-redis"})


def health(request):
    return JsonResponse({"status": "ok"})


def cache_check(request):
    try:
        value = cache.get_or_set(
            "hatch:preview:value",
            lambda: f"set-at-{int(time.time())}",
            timeout=300,
        )
        if cache.get("hatch:preview:hits") is None:
            cache.set("hatch:preview:hits", 0, timeout=300)
        hits = cache.incr("hatch:preview:hits")
        return JsonResponse({"cache": "ok", "value": value, "hits": hits})
    except Exception as e:
        return JsonResponse({"cache": "error", "error": str(e)}, status=500)
