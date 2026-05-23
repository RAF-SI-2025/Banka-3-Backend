# banka-raf

Projekat iz predmeta na RAF-u. Go/gRPC mikroservisi + Postgres.

Aktuelni stack u `newestbackend` sada ukljucuje:
- PostgreSQL primary + physical read replica (`postgres` + `postgres_replica`)
- mesecno particionisanu `listing_daily_price_info` tabelu sa sopstvenom maintenance funkcijom `ensure_listing_daily_price_partitions(...)`
- hash-particionisane Celina 5 interbank audit tabele po `sender_routing_number`
- dodatni read-heavy indeksi za transaction history, kreditne cron upite i trading/OTC portale
- InfluxDB za time-series berzanske podatke

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
