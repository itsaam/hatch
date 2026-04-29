# static — Hatch template

Minimal static site served by nginx alpine, ready for [Hatch](https://github.com/hatchpr) preview deployments.

## Stack

- nginx:alpine
- A single `index.html`

## Deploy on Hatch

1. Fork this repo.
2. Push a branch / open a PR — Hatch builds the image and exposes a preview URL.

That's it. No secrets, no DB, no env vars.

## Local preview

```bash
docker build -t static-preview .
docker run --rm -p 8080:80 static-preview
open http://localhost:8080
```
