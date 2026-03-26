# banka-raf

Projekat iz predmeta na RAF-u. Go/gRPC mikroservisi + Postgres.

## API

Api specifikacija se nalazi [ovde](https://ivan-klikovac.github.io/raf-banka3-api/)

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
|----------------|----------------------------------------------|
| `make all`     | Pokreni sve (proto, up, schema, seed)        |
| `make up`      | Pokreni servise/containere                   |
| `make down`    | Ugasi servise/containere                     |
| `make down-v`  | Ugasi i obrisi volume (cist start)           |
| `make proto`   | Regenerisi .pb.go fajlove u /gen             |
| `make schema`  | Load db schema                               |
| `make seed`    | Ucitaj test podatke                          |
| `make nuke`    | Obrisi sve i ucitaj schema i seed            |
| `make build`   | Builduj sve servise (Docker)                 |
| `make build-l` | Builduj sve servise (lokalno)                |
| `make test`    | Pokreni testove sa race detektorom (Docker)  |
| `make test-l`  | Pokreni testove sa race detektorom (lokalno) |
| `make fmt`     | Formatiraj kod sa gofmt (Docker)             |
| `make fmt-l`   | Formatiraj kod sa gofmt (lokalno)            |
| `make lint`    | Pokreni linter (Docker)                      |
| `make lint-l`  | Pokreni linter (lokalno)                     |

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
U repozitorijum je dodat .env.example.gpg. Ovo je fajl sa simetricnom enkripcijom
ciju sifru mozete na discordu u vidu pinnovane poruke. Sadrzaj ovog fajla ce
takodje biti dostupan na discordu tako da korisnik nije primoran da koristi gpg.

Za one koji imaju gpg (verovatno svi koji koriste linux, potreban je za package management)
dovoljno je izvrsiti sledecu komandu kako bi dekriptovali fajl:
`gpg --decrypt -o .env .env.example.gpg` nakon cega ce gpg promptovati za sifru preko GUI-a
ili TUI-a u zavisnosti od podesavanja.

Emacs korisnici takodje mogu koristiti (epa) EasyPG Assistant, za automatsko enkriptovanje
i dekriptovanje fajlova kao i dired integracije.
