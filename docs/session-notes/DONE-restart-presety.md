# DONE — restart + presety (brief-restart-presety)

Zrobione: `/restart [fresh|with-summary]` w TUI (ten sam skład i modele w nowym pokoju, skrót starego pokoju ≤30 linii jako jego pierwsza wiadomość), odblokowane tworzenie pokoju tym samym składem po jego usunięciu oraz start predefiniowanego pokoju — gateway odmawiał id-kolizji kodem 4110, teraz id jest bite na świeżo, a preset podąża za pokojem, którego faktycznie startował, do tego `rooms from <preset>` działa z CLI i drukuje ścieżkę katalogu zamiast struktury.

Sprawdzone: `go test -count=1 ./...` → exit 0, 17 pakietów `ok`, 0 `FAIL` (rooms 88.8% / tui 75.7% pokrycia, próg 70%), `gofmt -l` i `go vet ./...` czysto, `hermes verify --json --skip-start` → ok=true, 5/5 faz; dowód live `/restart` PASS (`scratch/restart-live-evidence.md`, log `scratch/restart-live-run.log`) oraz repro przed/po na żywym gatewayze: 4110 → rc=0.

Nie ruszone: commity, inne worktree/profile/DB, pliki cudzych zadań; pokoje powstałe przy dowodach zostają (to destrukcyjne) — ich lista i komenda sprzątania są w raporcie sesyjnym.
