# banka-raf

Projekat iz predmeta na RAF-u. Go/gRPC mikroservisi + Postgres.

Aktuelni stack u `newestbackend` sada ukljucuje:
- PostgreSQL primary + physical read replica (`postgres` + `postgres_replica`)
- mesecno particionisanu `listing_daily_price_info` tabelu sa sopstvenom maintenance funkcijom `ensure_listing_daily_price_partitions(...)`
- hash-particionisane Celina 5 interbank audit tabele po `sender_routing_number`
- dodatni read-heavy indeksi za transaction history, kreditne cron upite i trading/OTC portale
- InfluxDB za time-series berzanske podatke
- Spark analytics pipeline koji cita sa read replike i pise dnevne agregate nazad u Postgres, sa Kubernetes `ScheduledSparkApplication` manifestima
- Kubernetes autoscaling bundle za `gateway` sa `Deployment` + `Service` + `HPA` + `PodDisruptionBudget`

Za pripremu fakultetskog klaster deployment-a pogledajte i
`k8s/faculty/README.md`.

## API

Api specifikacija se nalazi
[ovde](https://ivan-klikovac.github.io/raf-banka3-api/)

## Potrebno

- [Docker](https://docs.docker.com/get-docker/)
- [Make](https://www.gnu.org/software/make/) (`brew install make` - macos /
  `choco install make` - windows)
- [Go](https://go.dev/dl/) — samo ako koristite `-l` komande za lokalno
  pokretanje

## Pokretanje

Koristimo docker-compose, sa make-om. (Potrebno je da prvo napravite '.env' file
ili kopirate example.)

```bash
cp .env.example .env
```

## Komande

Sve komande koriste Docker po defaultu. Dodajte `-l` suffix za lokalno
pokretanje (potreban Go na sistemu).

| Komanda        | Opis                                         |
| -------------- | -------------------------------------------- |
| `make all`     | Pokreni sve (proto, up, schema, seed)        |
| `make up`      | Pokreni servise/containere                   |
| `make down`    | Ugasi servise/containere                     |
| `make down-v`  | Ugasi i obrisi volume (cist start)           |
| `make proto`   | Regenerisi .pb.go fajlove u /gen             |
| `make schema`  | Load db schema                               |
| `make seed`    | Ucitaj test podatke                          |
| `make refresh-partitions` | Otvori naredne mesece za range particije |
| `make verify-replica` | Proveri da li je replica u recovery/read-only modu |
| `make verify-partitions` | Ispisi aktivne particije za bonus tabele |
| `make verify-indexes` | Ispisi dodatne read-heavy indekse za DB tuning |
| `make spark-analytics-image` | Builduj Spark analytics image |
| `make spark-analytics-local` | Pokreni Spark analytics lokalno preko docker compose profila |
| `make verify-spark-analytics` | Ispisi poslednje Spark analytics agregate iz Postgresa |
| `make k8s-gateway-image` | Builduj lokalni gateway image za Kubernetes autoscaling demo |
| `make k8s-autoscaling-apply` | Apply gateway autoscaling manifesta na Kubernetes |
| `make k8s-autoscaling-status` | Ispisi deployment/service/HPA/PDB status |
| `make nuke`    | Obrisi sve i ucitaj schema i seed            |
| `make build`   | Builduj sve servise (Docker)                 |
| `make build-l` | Builduj sve servise (lokalno)                |
| `make test`    | Pokreni testove sa race detektorom (Docker)  |
| `make test-l`  | Pokreni testove sa race detektorom (lokalno) |
| `make fmt`     | Formatiraj kod sa gofmt (Docker)             |
| `make fmt-l`   | Formatiraj kod sa gofmt (lokalno)            |
| `make lint`    | Pokreni linter (Docker)                      |
| `make lint-l`  | Pokreni linter (lokalno)                     |

Replica je dostupna na host portu `${POSTGRES_REPLICA_PORT}` (default `5433`)
i sluzi za read-only repliku primarne baze. Schema/seed i dalje idu iskljucivo
na primary preko `make schema` i `make seed`.

Particionisanje nije uradjeno kao jednokratni SQL rename/migrate korak, nego
kao deo startne seme baze: parent tabela `listing_daily_price_info` se odmah
kreira kao range-partitioned, a maintenance funkcija unapred otvara archive +
tekuci i naredne mesece. To daje jednostavniji bootstrap i predvidljiviji
lokalni razvoj.

Dodatni tuning je fokusiran na query obrasce koji vec postoje u kodu:
- `payments` / `transfers`: istorija po nalogu i sortiranje po vremenu
- `loans` / `loan_request`: cron i pregled zahteva po statusu i datumu
- `orders`: execution queue + portal listing po statusu / placeru
- `external_otc_*`: korisnicki thread/contract listing sa status filterom

## Spark analytics

Analytics sloj je izdvojen u `analytics/spark/` i koristi isti read/write split
kao aplikacioni servisi:
- raw podaci se citaju sa `postgres_replica`
- kurirani agregati se upisuju na `postgres`

PySpark job racuna dnevne operativne metrike za:
- payments / transfers
- orders / order fills
- external OTC contracts
- top listings po dnevnom prometu

Lokalni dry-run ide preko compose profila:

```bash
make spark-analytics-local
make verify-spark-analytics
```

Za Kubernetes deployment dodate su dve putanje:
- `analytics/spark/k8s/`: `ScheduledSparkApplication` za klastere sa Spark operatorom
- `analytics/spark/k8s/vanilla/`: obican Kubernetes `CronJob` / `Job` koji vrti Spark pod bez dodatnog operatora

To omogucava i strogu demonstraciju zahteva "Spark podignut na Kubernetesu"
i praktican lokalni run na Docker Desktop Kubernetes-u.

## Kubernetes autoscaling

Autoscaling je implementiran nad `gateway` slojem u `k8s/autoscaling/gateway/`,
jer je to stateless HTTP ulaz u sistem i najprirodniji kandidat za horizontalno
skaliranje. Bundle ukljucuje:
- `Deployment` sa readiness/liveness probe-ovima
- `Service`
- `HorizontalPodAutoscaler` (`autoscaling/v2`)
- `PodDisruptionBudget`

Kubernetes deployment koristi lokalni image `banka-3-backend-gateway:latest` i
za demo se povezuje na vec podignute docker-compose gRPC servise preko
`host.docker.internal`.

Napomena: stvarno HPA skaliranje zahteva dostupan metrics API (`metrics-server`
ili ekvivalent). Ako metrics API nije instaliran, manifesti ce se uredno
kreirati, ali HPA nece moci da donosi CPU/memory odluke dok se metrics sloj ne
doda u klaster.

## CI

CI se pokrece automatski na pull request prema `main` grani. Proverava:

- **Format** — `gofmt` provera
- **Lint** — golangci-lint
- **Build** — kompajlira sve servise
- **Test** — testovi sa race detektorom
- **Proto staleness** — proverava da li su generisani proto fajlovi azurni
- **Schema check** — validira schema i seed na pravom Postgresu

## Nix (opciono)

Ako hocete jos kontrole za cli - skinite nix kao jedini dependency i runnujte
`nix develop` (skida nixpkgs za sve sto bi moglo da vam treba za local
development).

Alternativno, mozete samo da skinete ove packages iz `flake.nix` sa svog package
managera.

## Secrets

U repozitorijum je dodat .env.example.gpg. Ovo je fajl sa simetricnom
enkripcijom ciju sifru mozete na discordu u vidu pinnovane poruke. Sadrzaj ovog
fajla ce takodje biti dostupan na discordu tako da korisnik nije primoran da
koristi gpg.

Za one koji imaju gpg (verovatno svi koji koriste linux, potreban je za package
management) dovoljno je izvrsiti sledecu komandu kako bi dekriptovali fajl:
`gpg --decrypt -o .env .env.example.gpg` nakon cega ce gpg promptovati za sifru
preko GUI-a ili TUI-a u zavisnosti od podesavanja.

Emacs korisnici takodje mogu koristiti (epa) EasyPG Assistant, za automatsko
enkriptovanje i dekriptovanje fajlova kao i dired integracije.
