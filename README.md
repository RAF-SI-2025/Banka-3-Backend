# banka-raf

Projekat iz predmeta na RAF-u. Go/gRPC mikroservisi + Postgres.

## API

Api specifikacija se nalazi
[ovde](https://ivan-klikovac.github.io/raf-banka3-api/)

## Potrebno

- [Docker](https://docs.docker.com/get-docker/)
- [Task](https://taskfile.dev) — `brew install go-task` / `nix develop` /
  [download](https://github.com/go-task/task/releases)
- [Go](https://go.dev/dl/) — za sve sto nije `task up`/`task down`/`task proto`
- [golangci-lint](https://golangci-lint.run/) — za `task lint`

## Pokretanje

Napravite `.env` (ili kopirajte example), pa pokrenite bootstrap:

```bash
cp .env.example .env
task setup     # proto + up + schema + seed
```

`task` bez argumenata izlistava sve dostupne komande.

## Komande

| Komanda                    | Opis                                                  |
| -------------------------- | ----------------------------------------------------- |
| `task setup`               | Bootstrap (proto + up + db:apply)                     |
| `task up`                  | Pokreni stack                                         |
| `task down`                | Ugasi stack (`task down -- -v` brise volume)          |
| `task reset`               | Cist start: down -v + up + db:apply                   |
| `task logs -- bank`        | Prati logove jednog servisa                           |
| `task db:apply`            | Schema + seed                                         |
| `task db:schema`           | Samo schema                                           |
| `task db:seed`             | Samo seed                                             |
| `task db:wipe`             | Drop + recreate public schema                         |
| `task db:psql`             | Otvori psql shell u kontejneru                        |
| `task proto`               | Regenerisi pkg/proto                                  |
| `task fmt`                 | gofmt -w                                              |
| `task fmt:check`           | gofmt -l (read-only, exit nonzero ako nesto fali)     |
| `task tidy`                | go mod tidy po modulima                               |
| `task tidy:check`          | tidy + git diff (CI)                                  |
| `task lint`                | golangci-lint po modulima                             |
| `task build`               | go build po modulima                                  |
| `task test`                | unit + integration                                    |
| `task test:unit`           | samo unit                                             |
| `task test:integration`    | samo integration                                      |
| `task workspace:check`     | provera da go.work pokriva sve module                 |
| `task nix:hashes`          | regenerisi vendorHashes u utils/nix/services.nix      |

Komande nad modulima (`fmt:check`, `tidy`, `tidy:check`, `lint`, `build`,
`test`, `test:unit`, `test:integration`) primaju imena modula posle `--`:

```bash
task lint -- bank             # samo bank
task test:unit -- bank user   # bank i user
task lint                     # svi moduli (pkg + svi servisi)
```

### Integration testovi — konvencija

Integration testovi treba da:

1. budu u fajlovima sa `//go:build integration` na vrhu, i
2. imaju funkcije sa prefiksom `TestIntegration_`.

Tako:

- `task test:unit` — kompajlira samo netagovane fajlove → samo unit testovi
- `task test:integration` — kompajlira sve, ali pokrece samo `TestIntegration_*`
- `task test` — pokrece sve sa `-tags=integration`

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
