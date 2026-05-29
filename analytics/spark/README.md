# Spark analytics

Ovaj direktorijum sadrzi batch analytics pipeline za `newestbackend`.

Pipeline:
- cita operativne tabele sa PostgreSQL read replike preko JDBC
- racuna dnevne KPI agregate za payments, transfers, orders, fills i OTC contracts
- upisuje kurirane rezultate nazad na primary u:
  - `analytics_daily_platform_metrics`
  - `analytics_daily_top_listings`

## Lokalni dry-run

Dok je docker-compose stack podignut:

```bash
make spark-analytics-local
make verify-spark-analytics
```

`spark_analytics` koristi isti PySpark job kao Kubernetes deployment, ali ga
pokrece u `local[2]` modu da bi pipeline mogao da se proveri bez klastera.

## Kubernetes

Postoje dve Kubernetes varijante:

1. `analytics/spark/k8s/`
: Spark Operator varijanta sa `ScheduledSparkApplication`

2. `analytics/spark/k8s/vanilla/`
: obican Kubernetes `CronJob` / `Job` koji vrti `spark-submit` u Spark podu,
  bez dodatne operator instalacije. Ovo je prakticniji put za Docker Desktop
  Kubernetes i lokalnu demonstraciju.

### Vanilla Kubernetes putanja

1. ukljuci Docker Desktop Kubernetes
2. build image lokalno:

```bash
docker compose build spark_analytics
```

3. proveri ili prilagodi `db-secret.template.yaml`
4. deploy cron varijantu:

```bash
kubectl apply -k analytics/spark/k8s/vanilla
```

5. za jednokratni run:

```bash
kubectl apply -f analytics/spark/k8s/vanilla/job-once.yaml
kubectl get jobs -n banka-analytics
kubectl logs -n banka-analytics job/banka-trading-analytics-once
```

### Spark Operator putanja

Ako u klasteru vec postoji `spark-operator`, moze i jaca varijanta:

```bash
kubectl apply -k analytics/spark/k8s
```
