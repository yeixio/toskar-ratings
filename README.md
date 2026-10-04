# Toskar community ratings

The hosted service behind [Toskar](https://github.com/yeixio/yggdrasil-core)'s community model ratings ([yggdrasil-core#37](https://github.com/yeixio/yggdrasil-core/issues/37)). It answers one question:

> **How well does this model work for people with hardware like mine?**

People rate a model from 1 to 5 stars, optionally with reasons such as "fast" or "crashed", and, if they agree, the speed and stability Toskar observed. Each rating is tied to:
- the model, format, quantization, runtime, and backend;
- a coarse hardware class.

Ratings are read back by similarity, narrowest first:

| Tier | Cohort | Example |
| --- | --- | --- |
| exact | accelerator and memory band | `apple:m4-max:unified:32-64` |
| family | accelerator model | `apple:m4-max` |
| class | accelerator class and memory band | `apple:apple-silicon:32-64`, `nvidia:rtx-40:16-32` |
| backend | runtime backend and memory band | `metal:32-64` |
| global | everyone | |

Scores are confidence-weighted, so one 5-star rating does not outrank hundreds of 4.8s. The formula is a Bayesian average: `(5 × prior + sum of stars) / (5 + ratings)`. The prior is the overall mean once there are 50 ratings, and 3.5 before that. Labels follow the sample size: `limited` (1–2), `early` (3–9), and `community` (10+).

## Privacy

Toskar asks before sending anything. A rating carries the stars, the tags, the model configuration, and a hardware class such as "Apple M4 Max, 32–64 GB". Observations are sent only if the person allows them, and so is the language they used the model in, such as `es`.

Never sent or stored:
- prompts, responses, or files;
- computer names, usernames, node IDs, MAC or IP addresses, or serial numbers.

The `client_id` is random, made once per installation, and lets a person update or delete their own rating. The service stores only a keyed hash of it. Addresses are used only for rate limits and batch tags, never stored: a batch tag is the hour plus a keyed hash of the network prefix, so an abusive flood can be found and invalidated.

The public aggregates have no per-person records. A cohort is published only with at least 3 ratings, and exact-hardware cohorts are never published. Ratings by language are published per model configuration, with the same minimum, and never split by hardware. A daily snapshot goes to [yeixio/toskar-model-data](https://github.com/yeixio/toskar-model-data).

## API

| Method | Path | |
| --- | --- | --- |
| POST | `/v1/ratings` | Submit a rating ([schema](schema/rating-v1.schema.json)). Rating the same configuration again from the same `client_id` updates the rating. Returns `{key, created}`. |
| PUT | `/v1/ratings/{key}` | Change stars, tags, observations, or language: `{client_id, stars, tags, observations, language}` |
| DELETE | `/v1/ratings/{key}` | Delete a rating; header `X-Ratings-Client: <client_id>` |
| GET | `/v1/models/{model}/ratings?format=&quantization=&runtime=&backend=&hardware=` | One configuration's stats at every tier, narrowest first. `hardware` is the exact cohort key; without it, only the global tier is returned. |
| GET | `/v1/aggregates` | The public snapshot ([schema](schema/ratings-v1.schema.json)) |
| GET | `/healthz` | Liveness |
| GET | `/v1/admin/batches?since=` | Write batches by size, for spotting floods (bearer `RATINGS_ADMIN_TOKEN`) |
| POST | `/v1/admin/invalidate` | `{batch}` or `{key}`: stop counting those ratings |

Rate limits are 30 writes and 1,200 reads an hour per network (/24, or /48 for IPv6).

## Running

See [docs/deploy.md](docs/deploy.md). In short:

```bash
docker run -d -p 8080:8080 -v ratings:/data -e RATINGS_SECRET="$(openssl rand -hex 32)" ghcr.io/yeixio/toskar-ratings
```

`toskar-ratings snapshot -out ratings.json` writes the public aggregates from the database.

## Development

```bash
go test -race ./...
go run ./cmd/toskar-ratings serve   # needs RATINGS_SECRET and RATINGS_DB
```

Licensed under the GNU AGPL v3, like Toskar Core.
