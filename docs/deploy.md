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

### On Kubernetes (production)

Production runs on the yeixio DigitalOcean Kubernetes cluster, kept in step with [`k8s/overlays/prod`](../k8s/overlays/prod) on `main` by ArgoCD. It uses one replica, because the database is SQLite on a 1 GB `do-block-storage-retain` volume and the rate limits are in memory. The cluster's ingress-nginx and cert-manager (`letsencrypt-prod`) serve it at `https://ratings.toskar.ai`.

These steps are done once:

1. **Store the secrets in AWS SSM.** Generate them straight into SecureString parameters, so they're never shown or written to disk:

   ```bash
   aws ssm put-parameter --name /yggdrasil-ratings/prod/RATINGS_SECRET --type SecureString --value "$(openssl rand -hex 32)"
   aws ssm put-parameter --name /yggdrasil-ratings/prod/RATINGS_ADMIN_TOKEN --type SecureString --value "$(openssl rand -hex 32)"
   ```

   Never overwrite `RATINGS_SECRET`.
2. **Let the namespace read them.** Create the namespace, and a SecretStore named `aws-ssm` in it, the same as `pacificnorthnorthcom`'s, with its AWS credentials Secret. The [ExternalSecret](../k8s/base/external-secret.yaml) syncs both parameters into the Secret `yggdrasil-ratings` every hour.
3. **DNS.** In Route 53, add `A ratings.toskar.ai → 167.172.1.191` and `AAAA ratings.toskar.ai → 2604:a880:400:d1:0:1:64a4:7001`, the ingress load balancer's addresses.
4. **ArgoCD.** Run `kubectl apply -f k8s/argocd-application-prod.yaml`.

To release, push a `v*` tag. Once its image is built, set `newTag` in `k8s/overlays/prod/kustomization.yaml` to that tag and merge.

**Client addresses.** The rate limits and batch tags need each client's address. ingress-nginx sets `X-Forwarded-For` to the address it sees, and the service trusts it (`RATINGS_TRUST_PROXY=true`). DigitalOcean's load balancer hides client addresses until the PROXY protocol is on: until then, every client counts as one network. To turn it on, set both of these at the same time, since a mismatch breaks every site on the cluster:
- the controller Service annotation `service.beta.kubernetes.io/do-loadbalancer-enable-proxy-protocol: "true"`;
- `use-proxy-protocol: "true"` in the `ingress-nginx-controller` ConfigMap.

### With Docker Compose

Use [compose.example.yml](../compose.example.yml), with Caddy for TLS:

```bash
cp compose.example.yml compose.yml
printf 'RATINGS_SECRET=%s\nRATINGS_ADMIN_TOKEN=%s\n' "<secret>" "<admin token>" > .env
docker compose up -d
```

Point a DNS record for `ratings.toskar.ai` at the host. Caddy gets a certificate on the first request. Behind any other reverse proxy, set `RATINGS_TRUST_PROXY=true` so rate limits use the client's address from `X-Forwarded-For`. Without a proxy, leave it unset.

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

On Kubernetes, take a snapshot of the `yggdrasil-ratings-data` volume in DigitalOcean, or schedule one.

## 4. The public dataset

[yeixio/yggdrasil-model-data](https://github.com/yeixio/yggdrasil-model-data) pulls `GET /v1/aggregates` once a day with GitHub Actions, validates it against the schema, and commits it with that repository's own token. Set its `RATINGS_URL` repository variable to the service's address, such as `https://ratings.toskar.ai`, once the service is up. The service itself never holds a GitHub credential.

## 5. Abuse

```bash
curl -H "Authorization: Bearer $RATINGS_ADMIN_TOKEN" https://ratings.toskar.ai/v1/admin/batches
curl -H "Authorization: Bearer $RATINGS_ADMIN_TOKEN" -d '{"batch":"2026100212-ab12cd34ef56"}' https://ratings.toskar.ai/v1/admin/invalidate
```

A batch is an hour of writes from one network. Invalidated ratings stop counting at once. A new rating from the same client counts again.
