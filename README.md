# Toskar community ratings

The hosted service behind [Toskar](https://github.com/yeixio/toskar-core)'s community model ratings ([toskar-core#37](https://github.com/yeixio/toskar-core/issues/37)). It answers one question:

> **How well does this model work for people with hardware like mine?**

People rate a model from 1 to 5 stars, optionally with reasons such as "fast" or "crashed", and, if they agree, the speed and stability Toskar observed. Each rating is tied to:
- the model, format, quantization, runtime, and backend;
- a coarse hardware class.

Ratings come from computers and from phones and tablets. A phone or tablet rates on the on-device runtime `llama.rn` (backends `metal`, `opencl`, or `cpu`), so its ratings of a GGUF are never combined with a computer's ratings of the same file. Its hardware class is the chip maker, the chip, and a phone memory band (`0-4`, `4-8`, `8-12`, `12-16`, `16-24`, or `24+` GB), never the device model.

Ratings are read back by similarity, narrowest first:

| Tier | Cohort | Example |
| --- | --- | --- |
| exact | accelerator and memory band | `apple:m4-max:unified:32-64`, `qualcomm:snapdragon-8-gen-3:unified:12-16` |
| family | accelerator model | `apple:m4-max`, `apple:a17-pro` |
| class | accelerator class and memory band | `apple:apple-silicon:32-64`, `nvidia:rtx-40:16-32`, `apple:a-series:8-12`, `qualcomm:snapdragon-8:12-16` |
| backend | runtime backend and memory band | `metal:32-64`, `opencl:12-16` |
| global | everyone | |

Scores are confidence-weighted, so one 5-star rating does not outrank hundreds of 4.8s. The formula is a Bayesian average: `(5 × prior + sum of stars) / (5 + ratings)`. The prior is the overall mean once there are 50 ratings, and 3.5 before that. Labels follow the sample size: `limited` (1–2), `early` (3–9), and `community` (10+).

## Privacy

Toskar asks before sending anything. A rating carries the stars, the tags, the model configuration, and a hardware class such as "Apple M4 Max, 32–64 GB". Observations are sent only if the person allows them, and so is the language they used the model in, such as `es`.

Never sent or stored:
- prompts, responses, or files;
- computer names, usernames, node IDs, MAC or IP addresses, or serial numbers;
- a phone's or tablet's model name. Only chip names are accepted as its family, and its app and runtime versions must be plain numbers, so a rating that carries a device model is refused and nothing of it is stored.

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

Rate limits are 30 writes an hour per `client_id`, and, per network (/24, or /48 for IPv6), 300 writes and 1,200 reads an hour. The per-network write limit is a backstop against floods, set high enough for many phones behind one carrier NAT.

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

## Support Toskar

Toskar's community ratings are free and open source. If they're useful to you, you can [donate to Toskar](https://yeix.io/donate/toskar), once or monthly, by card. Donations pay for its development. Nothing is held back for donors.

[![Donate to Toskar](https://img.shields.io/badge/Donate-Support_Toskar-ff6a55?style=for-the-badge&logo=stripe&logoColor=white)](https://yeix.io/donate/toskar)

## Contributing

Bug reports, tests, and fixes are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) first, and look for issues labeled [good first issue](https://github.com/yeixio/toskar-ratings/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

Licensed under the GNU AGPL v3, like Toskar Core.
