# Faculty Kubernetes Deployment Prep

Ovaj dokument je priprema za fakultetski Kubernetes deployment i prati
zahteve sa vezbi:
- backend adrese i kredencijali moraju da budu promenljivi bez rebuild-a
- frontend backend adresa mora da bude promenljiva bez rebuild-a
- tim treba unapred da zna spisak svih komponenti koje koristi

Formalna lista komponenti za slanje nalazi se u `k8s/faculty/COMPONENTS.md`.

## Ready-To-Send komponentna lista

Ovo mozete skoro direktno da posaljete profesoru/asistentu:

```text
Koristimo sledece komponente:
- frontend
- gateway
- user-service
- bank-service
- exchange-service
- notification-service
- postgres
- redis

Opcione / bonus komponente koje mozemo ukljuciti ako bude prostora:
- postgres read replica
- influxdb
- spark analytics job
```

Ako na odbrani ne planirate da branite sve bonuse na klasteru, najbezbednije je
da kao osnovni deployment zadrzite:
- `frontend`
- `gateway`
- `user-service`
- `bank-service`
- `exchange-service`
- `notification-service`
- `postgres`
- `redis`

## Predlozeni service nazivi

Profesor je trazio URL-kompatibilne nazive servisa. Ovi nazivi su prirodni za
nas projekat:

- `frontend`
- `gateway`
- `user-service`
- `bank-service`
- `exchange-service`
- `notification-service`
- `postgres`
- `redis`
- `postgres-replica`
- `influxdb`
- `spark-analytics`

## Backend runtime konfiguracija

Backend je vec pripremljen za runtime konfiguraciju bez rebuild-a.

Najvazniji env parametri po servisu:

### `gateway`

- `USER_GRPC_ADDR`
- `NOTIFICATION_GRPC_ADDR`
- `BANK_GRPC_ADDR`
- `EXCHANGE_GRPC_ADDR`
- `TRADING_GRPC_ADDR`
- `BANK_INTERNAL_HTTP_URL`
- `BANK_INTERNAL_API_KEY`
- `INTERBANK_API_KEY`
- `INTERBANK_ROUTES`
- `BANK_ROUTING_NUMBER`

### `user-service`

- `DATABASE_URL`
- `DATABASE_READ_URL`
- `REDIS_ADDR`
- `REDIS_PASSWORD`
- `NOTIFICATION_GRPC_ADDR`
- `ACCESS_JWT_SECRET`
- `REFRESH_JWT_SECRET`
- `PASSWORD_RESET_BASE_URL`
- `PASSWORD_SET_BASE_URL`
- `TOTP_DISABLE_BASE_URL`

### `bank-service`

- `DATABASE_URL`
- `DATABASE_READ_URL`
- `NOTIFICATION_GRPC_ADDR`
- `EXCHANGE_GRPC_ADDR`
- `USER_SERVICE_ADDR`
- `BANK_INTERNAL_API_KEY`
- `BANK_DEBUG_HTTP_PORT`
- `INFLUX_URL`
- `INFLUX_TOKEN`
- `INFLUX_ORG`
- `INFLUX_BUCKET`

### `exchange-service`

- `DATABASE_URL`
- `DATABASE_READ_URL`
- `EXCHANGE_RATE_API_KEY`

### `notification-service`

- `FROM_EMAIL`
- `FROM_EMAIL_PASSWORD`
- `FROM_EMAIL_SMTP`
- `SMTP_ADDR`

## Frontend runtime konfiguracija

Frontend je pripremljen tako da cita runtime vrednost iz:

- `window.__APP_CONFIG__.API_BASE_URL`

Tu vrednost u Docker/Nginx varijanti generise startup skripta iz env promenljive:

- `APP_API_BASE_URL`

To znaci da frontend backend adresu menja bez rebuild-a.

### Preporucene vrednosti za klaster

Ako frontend ide preko istog domena i zasebnog path-a ka gateway-u:

```text
APP_API_BASE_URL=https://<domen>/gateway/api
```

Ako frontend ingress radi rewrite tako da browser vidi samo `/api`:

```text
APP_API_BASE_URL=/api
```

Druga varijanta je elegantnija, ali zavisi od toga kako cete na vezbama
dogovoriti ingress routing.

## Primer internog DNS povezivanja na klasteru

Ako svi servisi budu u istom namespace-u, tipicne interne adrese mogu biti:

```text
USER_GRPC_ADDR=user-service:50051
NOTIFICATION_GRPC_ADDR=notification-service:50051
BANK_GRPC_ADDR=bank-service:50051
EXCHANGE_GRPC_ADDR=exchange-service:50051
TRADING_GRPC_ADDR=bank-service:50051
BANK_INTERNAL_HTTP_URL=http://bank-service:50090
DATABASE_URL=postgres://USER:PASSWORD@postgres:5432/banka?sslmode=disable
DATABASE_READ_URL=postgres://USER:PASSWORD@postgres-replica:5432/banka?sslmode=disable
REDIS_ADDR=redis:6379
INFLUX_URL=http://influxdb:8086
```

## Minimalni deployment za vezbe

Ako hocete najmanje rizican setup za prvi prolaz na klasteru, krenite sa:

1. `postgres`
2. `redis`
3. `user-service`
4. `notification-service`
5. `exchange-service`
6. `bank-service`
7. `gateway`
8. `frontend`

Bonuse poput `postgres-replica`, `influxdb` i `spark-analytics` ukljucite tek
ako budete sigurni da za njih imate dovoljno vremena i resursa na klasteru.

## Gde je sta vec implementirano

Backend runtime env podrska:
- `docker-compose.yml`
- `services/*/cmd/main.go`
- `services/gateway/internal/gateway/server.go`

Frontend runtime env podrska:
- `newestfrontend/Banka-3-Frontend/src/services/api.js`
- `newestfrontend/Banka-3-Frontend/public/app-config.js`
- `newestfrontend/Banka-3-Frontend/docker-entrypoint.d/40-write-app-config.sh`

## Najvaznija napomena za vezbe

Lokalni demo Kubernetes fajlovi za bonuse koriste i lokalne pogodnosti kao sto je
`host.docker.internal`. To je dobro za dokaz bonusa, ali nije finalni fakultetski
deployment format. Za klaster deployment koristite servisne DNS nazive unutar
namespace-a i runtime env konfiguraciju iz ovog dokumenta.
