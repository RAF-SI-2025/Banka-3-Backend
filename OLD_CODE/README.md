# banka-raf

Projekat iz predmeta na RAF-u. Go/gRPC mikroservisi + Postgres.

## Pokretanje

Koristimo docker-compose, sa make-om. (Potrebno je da prvo napravite '.env' file
ili kopirate example.)

```bash
cp .env.example .env
```

## Komande

| Komanda       | Opis                                  |
| ------------- | ------------------------------------- |
| `make all`    | Pokreni sve (proto, up, schema, seed) |
| `make up`     | Pokreni servise/containere            |
| `make down`   | Ugasi servise/containere              |
| `make down-v` | Ugasi i obrisi volume (cist start)    |
| `make proto`  | Regenerisi .pb.go fajlove u /gen      |
| `make schema` | Load db schema                        |
| `make seed`   | Ucitaj test podatke                   |
| `make nuke`   | Obrisi sve i ucitaj schema i seed     |
| `make lint`*  | Pokreni linter (kasnije)              |
| `make test`*  | Pokreni tester (kasnije)              |

## Nix (opciono)

Ako hocete jos kontrole za cli - skinite nix kao jedini dependency i runnujte
`nix develop` (skida nixpkgs za sve sto bi moglo da vam treba za local
development).

Alternativno, mozete samo da skinete ove packages iz `flake.nix` sa svog package
managera.
