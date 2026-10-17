# CS6650 HW5: Product API on AWS ECS with Terraform

A Go/Gin implementation of the **Product** endpoints from the course `api.yaml`,
deployed to AWS ECS Fargate with Terraform, load tested with Locust, and
instrumented with Prometheus RED metrics and structured JSON logs.

Forked from [RuidiH/CS6650_2b_demo](https://github.com/RuidiH/CS6650_2b_demo).

## Where everything is

| Path | What it contains |
|---|---|
| `src/product-service.go` | The Product API: `GET /products/{productId}` and `POST /products/{productId}/details`, a thread-safe in-memory store, and input validation |
| `src/observability.go` | Prometheus RED metrics at `/metrics` and one JSON log line per request (Part V) |
| `src/Dockerfile` | Multi-stage build; always produces `linux/amd64` for Fargate unless `TARGET_ARCH` says otherwise |
| `terraform/` | Infrastructure as code: `provider.tf`, `variables.tf`, `main.tf`, `outputs.tf`, and the `network`, `ecr`, `logging` and `ecs` modules |
| `scripts/get-ip.sh` | Prints the public IP of the running ECS task |
| `scripts/test-responses.sh` | Sends one request per test case and checks every response code in `api.yaml` |
| `load-test/` | Locust test (`locustfile.py`), its docker-compose file, and CSV results from every run (Part IV) |
| `observability/` | Local Prometheus + Grafana stack with a provisioned RED dashboard (Part V) |
| `docs/` | Screenshots and the request log excerpt referenced below |

## Deploy it on another machine

### Prerequisites

- **AWS CLI v2** with credentials for an account that has a `LabRole` IAM role
  (AWS Academy Learner Lab: copy the block from **AWS Details → AWS CLI** into
  `~/.aws/credentials`, including the `[default]` line). Region: `us-west-2`.
- **Terraform** 1.5 or newer (tested with 1.16.4).
- **Docker** running. Terraform builds and pushes the image itself.

### Steps

```bash
git clone https://github.com/sylfhen/CS6650_2b_demo.git
cd CS6650_2b_demo/terraform
terraform init       # downloads the AWS and Docker providers pinned in .terraform.lock.hcl
terraform apply      # review the plan (8 resources), then type "yes"
cd ..

IP=$(./scripts/get-ip.sh)                          # waits for the task, prints its public IP
curl "http://${IP}:8080/health"                    # {"status":"ok"}
SKIP_500=1 ./scripts/test-responses.sh "http://${IP}:8080"   # 17 passed, 0 failed
```

`apply` takes about 2-4 minutes. The image is built for `linux/amd64` on any
machine (including Apple Silicon), so it runs on Fargate.

**After changing code in `src/`, run `terraform apply` again.** Terraform hashes
every file in `src/`, rebuilds and pushes the image when the hash changes, and
the ECS service rolls out a new task using the new image digest.

**To remove everything:** `cd terraform && terraform destroy`. The ECR
repository is deleted even if it still holds images (`force_delete = true`).

## What I changed from the starter repo

| Change | Why |
|---|---|
| Replaced `src/main.go` (albums) with `product-service.go`, `observability.go`, `go.mod`, `go.sum` | The assignment |
| `src/Dockerfile`: cross-compiles with `GOARCH=${TARGET_ARCH}` (default `amd64`) and pins the run stage to `linux/${TARGET_ARCH}` | Docker on an Apple Silicon Mac builds `arm64` by default, which fails on Fargate (`x86_64`) with `exec format error` |
| `terraform/main.tf`: the task definition uses `repository@sha256:<digest>` instead of `:latest` | A new digest means a new task definition revision, so ECS actually deploys new code; it also makes ECS wait until the push has finished |
| `terraform/main.tf`: `triggers = { src_hash = ... }` on the image build and push | Rebuild and re-push whenever any file in `src/` changes (the image name alone never changes) |
| `terraform/modules/ecr/main.tf`: `force_delete = true` | `terraform destroy` otherwise fails on a repository that contains images |
| `.gitignore`: saved plans, local binaries, logs, Python bytecode | Keep sensitive and generated files out of the repository |

## Server configuration

| Environment variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | Listening port (matches `container_port` in `terraform/variables.tf`) |
| `SEED_PRODUCTS` | `1000` | Products 1..N created at startup (the spec has no create endpoint) |
| `ENABLE_FAULT_INJECTION` | off | When `true`, a request with `X-Simulate-Error: 500` returns 500. **Off on ECS.** |
| `LOG_REQUESTS` | on | Set to `false` to stop writing one JSON log line per request |

### Run it locally

```bash
cd src
PORT=8081 ENABLE_FAULT_INJECTION=true go run .            # or:
docker build --build-arg TARGET_ARCH=arm64 -t product-service:local .   # arm64 = native on Apple Silicon
docker run --rm -p 8081:8080 -e ENABLE_FAULT_INJECTION=true product-service:local
../scripts/test-responses.sh http://localhost:8081       # 19 passed, 0 failed
```

## Every response code

`scripts/test-responses.sh` sends one request per case and checks the status
against `api.yaml`: **19 checks locally** (fault injection on) and **17 on ECS**
(the two 500 checks are skipped with `SKIP_500=1`; fault injection is off there).

| Endpoint | Code | Triggered by |
|---|---|---|
| `GET /products/{productId}` | **200** | An existing product (1-1000 are seeded at startup) |
| | **404** | A product that does not exist, a non-integer ID, or ID 0 |
| | **500** | `X-Simulate-Error: 500` (only with `ENABLE_FAULT_INJECTION=true`) |
| `POST /products/{productId}/details` | **204** | A valid body for an existing product (no response body) |
| | **400** | Malformed JSON, empty body, missing field, wrong type, value out of range, `sku` over 100 characters, body `product_id` different from the path, non-integer path ID |
| | **404** | A valid body for a product that does not exist |
| | **500** | `X-Simulate-Error: 500` (only with `ENABLE_FAULT_INJECTION=true`) |

Every error uses the spec's `Error` schema: `{"error": ..., "message": ..., "details": ...}`.

### Examples

Set `BASE=http://localhost:8081` (local) or `BASE=http://${IP}:8080` (ECS).

```bash
curl -i "$BASE/products/1"                       # 200
# {"product_id":1,"sku":"SKU-000001","manufacturer":"Acme Corporation","category_id":2,"weight":101,"some_other_id":2}

curl -i "$BASE/products/999999"                  # 404
# {"error":"NOT_FOUND","message":"Product not found","details":"no product with ID 999999"}

curl -i -X POST "$BASE/products/1/details" -H 'Content-Type: application/json' \
  -d '{"product_id":1,"sku":"ABC-123-XYZ","manufacturer":"Acme Corporation","category_id":456,"weight":1250,"some_other_id":789}'
# 204 (no body); GET /products/1 now shows the new values

curl -i -X POST "$BASE/products/1/details" -H 'Content-Type: application/json' \
  -d '{"product_id":1,"sku":"A","manufacturer":"M","category_id":1,"some_other_id":1}'
# 400 {"error":"INVALID_INPUT","message":"Invalid input data","details":"weight is required"}

curl -i -X POST "$BASE/products/999999/details" -H 'Content-Type: application/json' \
  -d '{"product_id":999999,"sku":"A","manufacturer":"M","category_id":1,"weight":1,"some_other_id":1}'
# 404 {"error":"NOT_FOUND","message":"Product not found","details":"no product with ID 999999"}

curl -i -H 'X-Simulate-Error: 500' "$BASE/products/1"   # 500 (local, fault injection on)
# {"error":"INTERNAL_ERROR","message":"An unexpected error occurred","details":"see server logs for details"}
```

### Screenshots of every code

| Environment | Screenshots |
|---|---|
| `go run` (19/19) | [part 1](docs/screenshots/part2-go-run-tests-1.png), [part 2](docs/screenshots/part2-go-run-tests-2.png) |
| Docker (19/19) | [part 1](docs/screenshots/part2-docker-tests-1.png), [part 2](docs/screenshots/part2-docker-tests-2.png) |
| ECS via Terraform (17/17) | [part 1](docs/screenshots/part3-ecs-tests-1.png), [part 2](docs/screenshots/part3-ecs-tests-2.png) |

### Design decisions

- **GET has no 400 in the spec**, so an ID that can never exist (`abc`, `0`) returns 404.
- **POST only updates existing products** (confirmed by the instructor on Piazza), so a missing product is 404. The spec has no create endpoint, so products 1..`SEED_PRODUCTS` are seeded at startup.
- **POST checks in order:** path ID (400), body (400), existence (404), update (204).
- **A body `product_id` that differs from the path is 400.** The spec is silent here; accepting it would update a different product than the URL names.
- **Missing vs. zero:** fields decode into pointers, so a missing `weight` is 400 but `weight: 0` (the spec's minimum) is valid. Gin's `binding:"required"` would reject `0`.
- **500s go through a recovery handler** that returns the `Error` schema and logs the panic without leaking details to the client.
- **Routes are served with and without `/v1`**, since the spec's `servers` URLs end in `/v1`.
- **No authentication:** the spec lists `security` schemes, but neither Product endpoint defines a 401.

### Data structure and concurrency

Products live in a `map[int32]Product` guarded by a `sync.RWMutex`:

- **GETs take a read lock**, so any number run in parallel. **POSTs take the write lock.**
- In `Update`, the existence check and the write happen **under one lock**, so no
  other request can change the product in between (no check-then-act race).
- **Without the mutex, Go's runtime detects concurrent map access and crashes the
  whole process** (`fatal error: concurrent map writes`), which is far worse than one
  failed request.
- A map gives O(1) lookups by ID, which suits a read-heavy store where every request
  names exactly one product.

Under the Part IV stress tests (about 300 POSTs/s mixed with about 2,800 GETs/s), there
were **0 failures across 955,985 requests**, and 0 across 1,660,128 in the 4-worker run.

## Part IV: Load and stress testing (Locust)

Run from `load-test/` (`USER_CLASS`, `WAIT_MODE`, `LOAD_SHAPE` and `RUN_NAME` are optional):

```bash
TARGET_HOST=http://<ip>:8080 RUN_NAME=my-run docker compose up -d --scale worker=4
open http://localhost:8089        # results are saved to load-test/results/<RUN_NAME>_*.csv
```

**Request mix** (a store is mostly browsing): 85% GET an existing product, 5% GET a
missing product (404 counted as success, since it is the correct answer), 10% POST
details. Product IDs are random, so reads and writes hit the same map concurrently.

| Run | Client | Load | Result |
|---|---|---|---|
| **A: baseline** (and rerun) | FastHttpUser, 4 workers | 100 users, 1-2 s think time, 3 min | **65 RPS**, median 29-32 ms, p95 45-49 ms, **0 failures** |
| **B1** | HttpUser, **1 worker** | 50 -> 1200 users, no wait, 60 s steps | Plateau at **~3,100 RPS** from 200 users; Locust warned its CPU was over 90% |
| **B2** | FastHttpUser, **1 worker** | same | **~6,000 RPS**; server CPU 97-100% from 400 users |
| **C: stress** | FastHttpUser, 4 workers | same | **~6,500-6,800 RPS**, p95 ~550 ms at 1200 users, **0 failures in 1,660,128 requests**, no client CPU warnings |

### Where the server started to struggle, and why

ECS `CPUUtilization` from CloudWatch (a percentage of the task's 0.25 vCPU; data in `load-test/results/cloudwatch-cpu-*.txt`):

| Run C step | RPS | Server CPU |
|---|---|---|
| 100 users | ~2,950 | 71% |
| **200 users** | **~4,850** | **98%** |
| 400 -> 1200 users | ~5,500 -> 6,800 | 98-100% |

**The server's 0.25 vCPU is the bottleneck.** CPU is effectively full at ~4,850 RPS
(200 users). After that, 6x more users added only ~40% more throughput while p95
went from ~65 ms to ~550 ms: requests queue instead of being served. By a latency
target such as "p95 under 200 ms", the limit is about **400 users / ~5,700 RPS**.

**It degrades gracefully: slower, never failing.** Go's HTTP server accepts every
connection and handles each request in its own goroutine, so overload becomes
queueing rather than errors. To go further: scale **up** (a bigger task) or **out**
(raise `ecs_count` behind a load balancer).

**Screenshots:**
[A stats](docs/screenshots/part4-run-a-baseline-stats-final.png),
[A charts](docs/screenshots/part4-run-a-baseline-charts-final.png),
[A rerun stats](docs/screenshots/part4-run-a-baseline-rerun-stats.png),
[A rerun charts](docs/screenshots/part4-run-a-baseline-rerun-charts.png),
[B1 stats](docs/screenshots/part4-run-b1-httpuser-stats.png),
[B1 charts](docs/screenshots/part4-run-b1-httpuser-charts.png),
[B2 stats](docs/screenshots/part4-run-b2-fasthttpuser-stats.png),
[B2 charts](docs/screenshots/part4-run-b2-fasthttpuser-charts.png),
[C stats](docs/screenshots/part4-run-c-stress-stats.png),
[C charts](docs/screenshots/part4-run-c-stress-charts.png),
[CPU B1/B2](docs/screenshots/part4-cloudwatch-cpu-b1-b2.png),
[CPU C](docs/screenshots/part4-cloudwatch-cpu-c.png).

### HttpUser vs FastHttpUser

Same tasks, same 1-worker stepped ramp; only the HTTP client differs.

| | B1: HttpUser | B2: FastHttpUser |
|---|---|---|
| Peak RPS | ~3,100 | **~6,000 (1.9x)** |
| Locust CPU warning | at **200** users | at **1200** users |
| Server CPU at peak | ~75% (never saturated) | **97-100%** |

**Why:** `HttpUser` uses python-requests (urllib3, several Python layers, many objects
per request). `FastHttpUser` uses geventhttpclient, whose HTTP parsing is in C, so it
spends far less CPU per request. **The difference only shows when the load generator
is the bottleneck.** In B1 the client ran out of CPU first (users / RPS = 129 ms per
cycle at 400 users vs ~50 ms measured response time; the gap is time spent inside
Locust). With enough workers (run C), the client stops mattering and both would hit
the same server ceiling.

### Trade-offs

**Which operations are most common?** Reads: shoppers browse far more than anyone
edits product details. That is why the store uses a **`sync.RWMutex`**: many GETs
hold the read lock at once, and only the rarer POSTs need exclusive access. If
writes became frequent, a single lock would serialize them; sharding the map by
product ID (or `sync.Map`, which is tuned for read-mostly keys) would spread the
contention.

**What the in-memory map does not handle:** data is lost on restart, and with
`ecs_count > 1` every task would hold its **own** copy, so a POST to one task would
not be visible on another. Scaling out needs shared storage, for example DynamoDB
keyed by `product_id` with a cache in front for the read-heavy path.

## Part V: Observability (RED signals)

`src/observability.go` exposes Prometheus metrics at `/metrics`, labeled by **route
template** (`/products/:productId`, not `/products/123`, to keep cardinality low):

| Signal | Metric |
|---|---|
| **Rate** | `http_requests_total{method,route,status}` (counter) |
| **Errors** | `http_request_errors_total{method,route,status}` (counter of non-2xx) |
| **Duration** | `http_request_duration_seconds{method,route}` (histogram; buckets start at 0.1 ms because handlers take ~0.01-0.1 ms) |

Run the local stack (app + Prometheus + Grafana) from `observability/`:

```bash
TARGET_ARCH=arm64 docker compose up -d --build   # omit TARGET_ARCH on x86 machines
open http://localhost:3000                       # dashboard "Product API: RED signals"
```

### Idle vs. loaded (Part IV stress shape, run against the local stack)

| Panel | Idle | Loaded |
|---|---|---|
| Rate | 0 req/s | ~12-16K req/s on `GET`, ~1.5K on `POST .../details` |
| Errors | 0% | **~5% status 404** (the expected "missing product" requests) and a **~0.1% blip of 500s** from 200 deliberate faults |
| Duration (server-side) | no data (no requests) | p50/p95 **~0.05-0.1 ms**; p99 up to ~3 ms at peak |

Screenshots: [idle](docs/screenshots/part5-grafana-idle.png),
[rate + errors](docs/screenshots/part5-grafana-rate-errors.png),
[errors + duration](docs/screenshots/part5-grafana-errors-duration.png),
[Locust stats](docs/screenshots/part5-local-locust-stats.png),
[Locust charts](docs/screenshots/part5-local-locust-charts.png).

**Two findings:** (1) Locust saw a median of 13 ms and a p95 of 69-100 ms, while the
server spent under 0.1 ms per request: almost all user-visible latency was queueing
and network outside the handler, which server-side metrics cannot see. (2) 200 real
500s were nearly invisible as a percentage at 14K RPS, so alerting should also watch
the **absolute** 5xx count. (Local numbers are not comparable to ECS: Locust and the
app shared one Mac.)

### Request logs from the same run

One JSON line per request (full excerpt: [`docs/part5-request-log-excerpt.txt`](docs/part5-request-log-excerpt.txt)):

```json
{"time":"2026-10-17T21:20:30.000504843Z","level":"INFO","msg":"request","method":"GET","route":"/products/:productId","path":"/products/127","status":200,"duration_ms":0.01,"bytes":121,"client_ip":"192.168.65.1"}
{"time":"2026-10-17T21:20:30.000547177Z","level":"INFO","msg":"request","method":"GET","route":"/products/:productId","path":"/products/587314","status":404,"duration_ms":0.006,"bytes":89,"client_ip":"192.168.65.1"}
{"time":"2026-10-17T21:21:02.475711345Z","level":"INFO","msg":"request","method":"GET","route":"/products/:productId","path":"/products/1","status":500,"duration_ms":7.602,"bytes":107,"client_ip":"192.168.65.1"}
```

While idle, the app wrote **no request lines at all**; at peak it wrote thousands per second.
Times are UTC (21:20 UTC = 14:20 in Vancouver). An improvement for production: log 5xx at
`ERROR` and 4xx at `WARN`, so "show me only errors" is a filter on `level`.

### Dashboard or logs?

**I look at the dashboard to notice that something is wrong and how big it is**
(error rate rising, latency climbing, traffic dropping) as it happens, across all
requests at once. **I look at the logs to find out exactly which requests failed and
why, after the fact**: the path, status and duration of individual requests, even if
nobody had Grafana open when the problem happened.
