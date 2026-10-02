# Deploying the ratings service

The service is one static binary with a SQLite database, shipped as a container image. It is small: a single instance on a 1 vCPU / 512 MB VPS handles far more than V1 needs.

## 1. Secrets

Make two random values and keep them somewhere safe, such as a password manager:

```bash
openssl rand -hex 32   # RATINGS_SECRET
openssl rand -hex 32   # RATINGS_ADMIN_TOKEN
```

**`RATINGS_SECRET` must never change.** It keys the stored client hashes and batch tags. A new secret would orphan every rating: people could no longer update them, and every update would create a duplicate.

## 2. Run it

With Docker Compose and Caddy for TLS (see [compose.example.yml](../compose.example.yml)):

```bash
cp compose.example.yml compose.yml
printf 'RATINGS_SECRET=%s\nRATINGS_ADMIN_TOKEN=%s\n' "<secret>" "<admin token>" > .env
docker compose up -d
```

Point a DNS record for `ratings.yggdrasil.yeix.io` at the host, and Caddy gets a certificate on first request. Behind any other reverse proxy, set `RATINGS_TRUST_PROXY=true` so rate limits use the client's address from `X-Forwarded-For`. Without a proxy, leave it unset.

The image is built and pushed to `ghcr.io/yeixio/yggdrasil-ratings` when a `v*` tag is pushed (`.github/workflows/release.yml`).

| Variable | Default | |
| --- | --- | --- |
| `RATINGS_ADDR` | `:8080` | Listen address |
| `RATINGS_DB` | `/data/ratings.db` | SQLite file; keep `/data` on a persistent volume |
| `RATINGS_SECRET` | (required) | 32+ characters; never change it |
| `RATINGS_ADMIN_TOKEN` | (off) | Bearer token for `/v1/admin` |
| `RATINGS_TRUST_PROXY` | `false` | Read the client address from `X-Forwarded-For` |

## 3. Back up

The database is the only state:

```bash
docker compose exec ratings /usr/local/bin/yggdrasil-ratings snapshot -out /data/snapshot.json   # the public view
sqlite3 /var/lib/docker/volumes/<project>_ratings-data/_data/ratings.db ".backup ratings-backup.db"
```

## 4. The public dataset

[yeixio/yggdrasil-model-data](https://github.com/yeixio/yggdrasil-model-data) pulls `GET /v1/aggregates` once a day with GitHub Actions, validates it against the schema, and commits it with that repository's own token. Set its `RATINGS_URL` repository variable to the service's address, such as `https://ratings.yggdrasil.yeix.io`, once the service is up. The service itself never holds a GitHub credential.

## 5. Abuse

```bash
curl -H "Authorization: Bearer $RATINGS_ADMIN_TOKEN" https://ratings.yggdrasil.yeix.io/v1/admin/batches
curl -H "Authorization: Bearer $RATINGS_ADMIN_TOKEN" -d '{"batch":"2026100212-ab12cd34ef56"}' https://ratings.yggdrasil.yeix.io/v1/admin/invalidate
```

A batch is an hour of writes from one network. Invalidated ratings stop counting at once. A new rating from the same client counts again.
