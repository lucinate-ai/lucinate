# WDROŻENIE — lucinate — 2026-11-04

Wdrożenie planu `audyty/lucinate-2026-11-04-poprawki.md` (audyt: `audyty/lucinate-2026-11-04.md`).
Zmiany zostawione w worktree — bez commita, zgodnie z regułą roli.

## 1. Co wdrożono

| ID | waga | zmiana | plik |
|---|---|---|---|
| AUD-001 | S2 | README: Hermes = `hermes dashboard` / WebSocket `/api/ws` / 9119; `/v1` jawnie odrzucony | `README.md:83,85` |
| AUD-002 | S2 | `turn.cancelled` terminalny w strumieniu i w budżecie dyskusji | `internal/rooms/stream.go:109-110`, `discussion.go:146` |
| AUD-003 | S2 | `200` bez SSE = błąd (typ + pierwsze bajty); zero ramek = błąd; `/compact` tak samo | `internal/backend/openai/backend.go:377-392,414-428,745-760` |
| AUD-004 | S2 | `ResponseHeaderTimeout` 60 s; watchdog ciszy 5 min; `--timeout` (10m, `0`=∞); SIGINT/SIGTERM anulują run | `httpcommon/client.go:21-26,138`, `openai/backend.go:305-331`, `app/send.go:63-68,199-205,239-242`, `cli/cli.go:36-40`, `cli/send.go:63-78`, `cli/ask.go:75-87` |
| AUD-005 | S3 | test bez wyścigu (kanał zamiast pollingu) + krok `go test -race` w CI | `internal/tui/app_test.go:393,399-406,420-426`, `.github/workflows/ci.yml:25-28` |
| AUD-019 | S3 | instalatory weryfikują `checksums.txt` przed instalacją | `install/lucinate.sh:33-52`, `install/lucinate.ps1:34-45` |
| AUD-020 | S3 | release czeka na testy (`needs: test`) | `.github/workflows/release.yml:11-24` |
| AUD-021 | S3 | README „Go 1.26+”, `.tool-versions` 1.26.0 | `README.md:59`, `.tool-versions:1` |
| AUD-022 | S3 | AGENTS.md i openspec context opisują realny replace na tag + nazwę modułu | `AGENTS.md:41`, `openspec/config.yaml:9,11` |
| AUD-024 | S3 | usunięty wpis w CHANGELOG o nieistniejącym `TestLive_RoomHandoff` | `CHANGELOG.md` |
| AUD-025 | S4 | zamknięte zdarzeniem po pomiarze (commit `9688279`, drzewo czyste) | — |

Kontrakt zachowania: `openspec/changes/honest-stream-failures/` (proposal, tasks, delta dla
`backend-openai`, `one-shot`, `rooms`). **Nie zarchiwizowane** — `openspec` CLI nie jest
zainstalowane w tym środowisku; `openspec archive honest-stream-failures` do uruchomienia tam,
gdzie CLI jest. `openspec/specs/**` nietknięte (zgodnie z regułą repo).

Dokumentacja „dlaczego": `docs/backend_openai.md` § „A 200 is not a reply", § „Bounded waits";
`docs/one-shot.md` § „The reply wait is bounded".

## 2. Czego nie wdrożono (świadomie)

| ID | dlaczego nie teraz |
|---|---|
| AUD-006 (drop eventów bez logu) | jedna linia, ale nie było w planie; kandydat na następny commit razem z licznikiem |
| AUD-007, 008 (klasyfikacja auth po substringu, `StatusConnected` bez dowodu) | wymaga zmiany protokołu zdarzeń w Hermesie — osobny change |
| AUD-009–013 (pokrycie CLI/OpenClaw/app, próg pokrycia, integracje OpenAI w CI) | próg i harness to decyzja zespołu; `test-integration-openai` wymaga usługi Ollama w workflow |
| AUD-014–018 (kopie reguł, rozproszone timeouty, `SendOpening` z git/brief/ledger) | refaktor, nie poprawka; ryzyko większe niż zysk bez testów charakterystycznych |

## 3. Odstępstwa od planu (z powodem)

1. **Idle watchdog 5 min, nie 2.** Lokalny serwer ładujący duży model potrafi milczeć kilka
   minut przed pierwszym tokenem; 2 min karałyby poprawny cold start. Wartość jest `var`, więc
   testy ją skracają, a produkcja ma jedną stałą.
2. **`--timeout` domyślnie 10 min.** Plan mówił „np. 10m"; przyjęte bez zmian. `0` = bez limitu.
   Flaga nie trafiła do `config.AskDefaults` — to bezpiecznik wywołania, nie preferencja.
3. **Sprawdzenie `Content-Type` tylko gdy nagłówek jest niepusty.** Serwer, który streamuje bez
   nagłówka, nadal działa; o błędzie decyduje wtedy licznik ramek. Odwrotna kolejność (twardy
   wymóg nagłówka) odrzucałaby działające, minimalistyczne implementacje.
4. **`/compact` (`summarise`) dostał te same guardy.** Ten sam kształt błędu; bez tego
   „pusty sukces" zostawał na ścieżce kompakcji.

## 4. Weryfikacja

```
gofmt -l .                                             # puste
go build ./...                                         # exit 0
go vet ./...                                           # exit 0
go test -count=1 ./...                                 # exit 0 (17 pakietów, tui 55.0 s)
go test -race -count=1 -timeout 15m ./...              # exit 0 (tui 59.0 s)
make build                                             # exit 0, wersja wstrzyknięta
./lucinate send --help                                 # pokazuje --timeout
sh -n install/lucinate.sh                              # exit 0
python3 -c yaml.safe_load(ci.yml, release.yml)         # 2 workflow, release needs: test
```

Testy: **1414** funkcji `Test` (było 1405; +9 nowych). Nowe:
`TestStreamsFor_CancelledTurnSettlesTheStream`, `TestStreamsFor_CancelledBeforeTextIsEmptyAndNotActive`,
`TestBackend_ChatSendRejectsANonEventStream`, `TestBackend_ChatSendEmptyEventStreamIsAnError`,
`TestBackend_ChatSendFinishOnlyChunkIsASuccess`, `TestBackend_ChatSendIdleStreamEmitsError`,
`TestNewDefaultTransport_BoundsResponseHeaders`, `TestNewAskFlagSet_TimeoutDefaultsAndOverrides`,
`TestSend_TimeoutReturnsErrorWithoutAReply`.

Weryfikacja sum kontrolnych instalatora na fixture: poprawny plik → `OK`; zmieniony → exit 1;
brak wpisu w `checksums.txt` → exit 1.

## 5. Ryzyka

- **`-race` w CI** wydłuża job o ~60 s (tui dominuje); przy flake'ach na runnerze to nowe źródło
  czerwonych buildów. Próg `-timeout 15m` zostawia zapas.
- **`ResponseHeaderTimeout` 60 s** dotyczy też Hermesa (wspólny transport). Endpoint, który
  naprawdę potrzebuje >60 s na nagłówki, teraz failuje — dla strumieni to pożądane, ale warto
  obserwować.
- **`--timeout` 10 min** może uciąć legalnie długi, ale *aktywny* run (długi tool-loop). Kto tego
  potrzebuje, podaje `--timeout 0`; TUI jest bez zmian.
- **Idle 5 min** nadal może uciąć bardzo wolny cold start (model 70B+ na wolnym dysku). Wartość
  jest jedną stałą — łatwa do podniesienia, jeśli w praktyce zajdzie potrzeba.
- **Zachowanie na granicy**: serwer wysyła poprawne SSE z `Content-Type: application/json` —
  teraz błąd. Niemal nie występuje, ale jest to zmiana zachowania; złagodzenie to jedno `if`.
- Niewdrożone S3 (sekcja 2) zostają w audycie jako otwarte; nic z nich nie udaje zamkniętego.
