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

These steps are done once. The service runs in the namespace `toskar-ratings-prod`, its database is the volume `toskar-ratings-data`, and its secrets are under `/toskar-ratings/prod/` in SSM.

1. **Store the secrets in AWS SSM** (`aws login` first if your session has expired). Generate them straight into SecureString parameters, so they're never shown or written to disk:

   ```bash
   aws ssm put-parameter --region us-east-1 --name /toskar-ratings/prod/RATINGS_SECRET --type SecureString --value "$(openssl rand -hex 32)"
   aws ssm put-parameter --region us-east-1 --name /toskar-ratings/prod/RATINGS_ADMIN_TOKEN --type SecureString --value "$(openssl rand -hex 32)"
   ```

   Never overwrite `RATINGS_SECRET`.
2. **Create the namespace and copy in the two credentials** the other apps on this cluster use. The commands copy each Secret without printing it: `aws-credentials` lets the [SecretStore](../k8s/base/secret-store.yaml) read SSM, and `ghcr-credentials` lets the cluster pull the private image.

   ```bash
   kubectl create namespace toskar-ratings-prod
   kubectl get secret aws-credentials -n pacificnorthnorthcom -o json \
     | jq 'del(.metadata.namespace,.metadata.resourceVersion,.metadata.uid,.metadata.creationTimestamp,.metadata.managedFields,.metadata.ownerReferences,.metadata.annotations)' \
     | kubectl apply -n toskar-ratings-prod -f -
   kubectl get secret ghcr-credentials -n toskar-ai-prod -o json \
     | jq 'del(.metadata.namespace,.metadata.resourceVersion,.metadata.uid,.metadata.creationTimestamp,.metadata.managedFields,.metadata.ownerReferences,.metadata.annotations)' \
     | kubectl apply -n toskar-ratings-prod -f -
   ```

   If that AWS key's IAM policy only allows its own app's parameters, also allow `ssm:GetParameter*` on `arn:aws:ssm:us-east-1:*:parameter/toskar-ratings/prod/*`, or make a separate key for this app. The [ExternalSecret](../k8s/base/external-secret.yaml) then syncs both parameters into the Secret `toskar-ratings` every hour.
3. **Publish the image.** Push the tag that `k8s/overlays/prod/kustomization.yaml` names (`v0.1.0` for the first deploy), and wait for the Release workflow to push `ghcr.io/yeixio/toskar-ratings`.
4. **DNS.** `ratings.toskar.ai` needs `A → 167.172.1.191` and `AAAA → 2604:a880:400:d1:0:1:64a4:7001`, the ingress load balancer's addresses (already set in Route 53).
5. **ArgoCD.** Run `kubectl apply -f k8s/argocd-application-prod.yaml`. ArgoCD creates the rest, and cert-manager gets the certificate.
6. **Check** that `curl https://ratings.toskar.ai/healthz` answers with a real certificate, and that `/v1/aggregates` returns the empty aggregates.

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

The image is built and pushed to `ghcr.io/yeixio/toskar-ratings` when a `v*` tag is pushed (`.github/workflows/release.yml`).

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
docker compose exec ratings /usr/local/bin/toskar-ratings snapshot -out /data/snapshot.json   # the public view
sqlite3 /var/lib/docker/volumes/<project>_ratings-data/_data/ratings.db ".backup ratings-backup.db"
```

On Kubernetes, take a snapshot of the `toskar-ratings-data` volume in DigitalOcean, or schedule one.

## 4. The public dataset

[yeixio/toskar-model-data](https://github.com/yeixio/toskar-model-data) pulls `GET /v1/aggregates` once a day with GitHub Actions, validates it against the schema, and commits it with that repository's own token. Set its `RATINGS_URL` repository variable to the service's address, such as `https://ratings.toskar.ai`, once the service is up. The service itself never holds a GitHub credential.

## 5. Abuse

```bash
curl -H "Authorization: Bearer $RATINGS_ADMIN_TOKEN" https://ratings.toskar.ai/v1/admin/batches
curl -H "Authorization: Bearer $RATINGS_ADMIN_TOKEN" -d '{"batch":"2026100212-ab12cd34ef56"}' https://ratings.toskar.ai/v1/admin/invalidate
```

A batch is an hour of writes from one network. Invalidated ratings stop counting at once. A new rating from the same client counts again.
