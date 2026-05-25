# Kubernetes manifests

Per the rewrite's CLAUDE.md, k8s manifests are written outside this
repo. The app code is k8s-ready (probes on `:PROBE_PORT`,
graceful shutdown on SIGTERM, structured JSON logs, env-only config),
so any standard deployment / service / HPA / PDB layout works.

## Bonus pointers

The main branch shipped two bonuses whose entire payload is k8s YAML:

* **BonusKubernetesAutoscaling (PR #289).** A horizontal pod
  autoscaler for the gateway, pinned to CPU + memory targets, with a
  PodDisruptionBudget and the metrics-server YAML for a Docker
  Desktop cluster. The manifests live under
  `k8s/autoscaling/gateway/` and `k8s/metrics-server/` on main —
  copy them to a private deployment repo, point the deployment image
  ref at the rewrite's gateway image, and `kubectl apply -k` them.
  The rewrite's gateway already exposes `/healthz` + `/readyz` +
  `/metrics` (BonusMLAObservability) on the same probe port, so no
  additional code change is needed.

* **BonusSparkKubernetesAnalytics (PR #288)** + **BonusSparkML
  (PR #291).** Spark Operator + vanilla CronJob manifests for the
  python jobs that live in `analytics/spark/jobs/`. Same drill —
  manifests live on main under `analytics/spark/k8s/`, copy them to
  the deployment repo, point the image ref at the analytics image
  built from `analytics/spark/Dockerfile`. The analytics tables also
  need a migration in the rewrite first; see `analytics/README.md`.

If you want the manifests authored in this repo, that's a CLAUDE.md
policy change. Today this README is the load-bearing artifact for
the three k8s-only bonuses.
