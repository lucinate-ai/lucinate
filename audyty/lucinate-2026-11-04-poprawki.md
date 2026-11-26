# POPRAWKI — lucinate — 2026-11-04

Plan naprawy dla audytu `audyty/lucinate-2026-11-04.md`. **To nie jest audyt i nic nie zostało zastosowane** — każda pozycja to `plik:linia`, szkic zmiany, test, który ma ją zamknąć, i komenda weryfikująca. Kolejność: S2 najpierw, potem tanie wygrane.

Stan bazowy: HEAD `4b2dd65`, `go build`/`go vet`/`go test ./...` zielone.

## 1. AUD-001 — README prowadzi Hermesa w martwy URL (S2, ~5 min)

**Plik:** `README.md:83` i `README.md:85`. Formularz TUI jest poprawny — nie ruszać.

Szkic:

```
- **[Hermes (Nous Research)](docs/backend_hermes.md)** — connect to a Hermes Agent profile's
  `hermes dashboard` gateway. One Lucinate connection maps to one Hermes profile; chat state
  lives server-side.

OpenClaw URLs can use `https`, `http`, `wss`, or `ws` — lucinate derives the WebSocket endpoint
automatically. OpenAI-compatible URLs are HTTP(S) base URLs ending in `/v1`. Hermes URLs are the
gateway's HTTP base URL (default `http://localhost:9119`); lucinate dials `/api/ws` and asks for
the gateway token on connect.
```

**Test:** brak testu kodu — to dokumentacja. Zamknięcie = `grep -n "/v1" README.md` nie trafia w sekcję Hermesa; `docs/backend_hermes.md:14` i `openspec/specs/backend-hermes/spec.md:11` już mówią poprawnie, więc README ma być z nimi spójne.

**Opcjonalnie:** etykieta pola `Base URL:` (`internal/tui/connections.go:175-178,864-867`) jest współdzielona z OpenAI i podtrzymuje pomyłkę; dla Hermesa mogłaby brzmieć `Gateway URL:`. Nie jest to warunek zamknięcia.

## 2. AUD-002 — `turn.cancelled` nie kończy strumienia (S2, ~30 min)

**Plik 1 (naprawa objawu):** `internal/rooms/stream.go:109-115`.

```go
case ev.Kind == "turn.settled", ev.Kind == "turn.failed", ev.Kind == "turn.deferred",
    ev.Kind == "turn.cancelled", ev.Kind == "member.unavailable":
```

**Plik 2 (symetria, defekt utajony):** `internal/rooms/discussion.go:146` — dołożyć `"turn.cancelled"` do `case "turn.failed", "turn.deferred":` (zdejmuje `openTurns`, oznacza `terminalInRound`). Dziś obie gałęzie dają ten sam wynik (`EndReasonNone == ""`), więc to nie zmienia zachowania — ma nie ożyć przy przyszłej zmianie.

**Test (regresja):** `internal/rooms/stream_test.go`, obok `TestActiveStreams...`:

```go
func TestActiveStreams_DropsACancelledTurn(t *testing.T) {
	events := []Event{
		stEvent(1, "turn.started", "matt", nil),
		stEvent(2, "turn.cancelled", "matt", nil),
	}
	if got := ActiveStreams(events); len(got) != 0 {
		t.Fatalf("ActiveStreams = %+v, want none after turn.cancelled", got)
	}
}
```

Test na poziomie TUI (opcjonalny, droższy): po `turn.started`→`turn.cancelled` `m.spinnerActive()` musi być `false` — `internal/tui/rooms_ux.go:204-207`.

**Weryfikacja:** `go test -count=1 ./internal/rooms/ -run 'Streams|Cancelled'`.

## 3. AUD-003 — 200 bez SSE raportowane jako sukces (S2, ~1 h)

**Plik:** `internal/backend/openai/backend.go:308-390` (`runStream`). Dwa warunki, oba przed `EmitChatFinal`:

1. Po `resp.StatusCode >= 400` (`:355`) sprawdzić typ odpowiedzi:

```go
if ct := resp.Header.Get("Content-Type"); !strings.Contains(strings.ToLower(ct), "text/event-stream") {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	b.emitter.EmitChatError(runID, sessionKey,
		fmt.Sprintf("expected text/event-stream, got %q (first bytes: %q)", ct, strings.TrimSpace(string(raw))))
	return
}
```

2. Policzyć ramki, które przeszły parsowanie (`frames++` w callbacku `:362-380`), i po `ScanSSE`:

```go
if frames == 0 {
	b.emitter.EmitChatError(runID, sessionKey, "stream ended without any data frame")
	return
}
```

Warunek `frames == 0`, nie `final == ""` — pusta odpowiedź modelu z poprawną ramką dalej jest sukcesem.

**Test:** `internal/backend/openai/backend_test.go` (wzór: `httptest.NewServer` w `:63,76,92`). Serwer zwraca `200` + `text/html`; asercja: event `chat` ze stanem `error`, brak `final`, `LoadHistory` bez wpisu asystenta.

**Weryfikacja:** `go test -count=1 ./internal/backend/openai/`.

## 4. AUD-004 — brak timeoutu nagłówków i ciszy w strumieniu (S2, ~2 h)

**Plik 1:** `internal/backend/httpcommon/client.go:124-139` (`NewDefaultTransport`) — dodać po `TLSHandshakeTimeout`:

```go
t.ResponseHeaderTimeout = 60 * time.Second
```

(Stała obok `connectTimeout`; nie wiązać z dial timeoutem — wolny model potrzebuje dłuższego okna na nagłówki niż na TCP.)

**Plik 2:** `internal/backend/openai/backend.go:294` — watchdog ciszy zamiast `context.Background()` bez granic:

```go
const idleLimit = 2 * time.Minute
var idleFired atomic.Bool
idle := time.AfterFunc(idleLimit, func() { idleFired.Store(true); cancel() })
defer idle.Stop()
// w callbacku ScanSSE, po odebraniu każdej ramki: idle.Reset(idleLimit)
```

W gałęzi `scanErr != nil` (`:381-385`) rozróżnić: gdy `idleFired.Load()`, komunikat „no data frame for 2m", nie surowy błąd sieci.

**Plik 3:** `internal/cli/cli.go:32` (`Run`) — Ctrl+C ma kończyć `send`/`ask`:

```go
ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
defer stop()
```

**Plik 4:** `internal/cli/send.go:60-71` (`newSendFlagSet`) + `internal/cli/ask.go` (`newAskFlagSet`): flaga `--timeout` (domyślnie np. `10m`), przekazana do `app.SendOptions`; w `app/send.go:193-231` owinąć oczekiwanie w `context.WithTimeout(ctx, opts.Timeout)`.

**Test:** `httptest` serwer, który śpi przed nagłówkami; `Send` z `Timeout: 200ms` musi zwrócić błąd w < 2 s, nie wisieć. Dla SSE: serwer wysyła nagłówki i jedną ramkę, potem milczy — przy skróconym `idleLimit` test musi dostać event `error`.

**Weryfikacja:** `go test -count=1 ./internal/backend/openai/ ./app/ ./internal/cli/`.

## 5. AUD-005 — wyścig w teście, CI bez `-race` (S3, ~30 min)

**Plik 1 (test):** `internal/tui/app_test.go:388-421`. `var publishedNil bool` zapisywany z goroutiny (`internal/tui/app.go:736`) i czytany w pętli `runtime.Gosched()`. Zamienić na kanał:

```go
published := make(chan struct{}, 1)
// w OnBackendChanged: if b == nil { published <- struct{}{} }
select {
case <-published:
case <-time.After(2 * time.Second):
	t.Error("expected OnBackendChanged(nil) to publish backend tear-down")
}
```

**Plik 2 (CI):** `.github/workflows/ci.yml`, po kroku `Test` (`:22-23`):

```yaml
      - name: Test (race)
        run: go test -race ./...
```

Koszt: pełny `-race` na tym drzewie to kilka minut; jeśli za drogo, minimum to `go test -race ./internal/rooms/ ./internal/backend/... ./internal/tui/`, ale wtedy `tui` i tak dominuje czas.

**Weryfikacja:** `go test -race -count=1 -run TestAppModel_RoutesShowConnectionsMsg ./internal/tui/` — musi być zielone; `grep -n "race" .github/workflows/ci.yml` — musi trafiać.

## 6. Tanie wygrane (S3, po ~5–10 min każda)

| ID | plik:linia | zmiana |
|---|---|---|
| AUD-019 | `install/lucinate.sh:32-39`, `install/lucinate.ps1:29-33` | pobrać `checksums.txt` z tego samego tagu i zweryfikować przed `install`/`Expand-Archive` (`sha256sum -c --ignore-missing`, `Get-FileHash`); przy braku pliku — odmówić instalacji |
| AUD-020 | `.github/workflows/release.yml:34-40` | dodać job `test` (`make test`) i `needs: test` przed GoReleaserem; tag nie publikuje nieprzetestowanej binarki |
| AUD-021 | `README.md:59`, `.tool-versions:1` | „Go 1.26+" (zgodnie z `go.mod:3`); `.tool-versions` → `golang 1.26.0` |
| AUD-022 | `AGENTS.md:41`, `openspec/config.yaml:11` | opisać realny `replace` (zdalny fork na tagu, `go.mod:61`) zamiast `../openclaw-go`; poprawić nazwę modułu na `github.com/lucinate-ai/lucinate` |
| AUD-024 | `CHANGELOG.md:16` | usunąć wpis o `TestLive_RoomHandoff` albo dopisać test o tej nazwie; deklaracja bez artefaktu nie może zostać |
| AUD-025 | — | **zamknięte po pomiarze**: commit `9688279` zacommitował trzy brudne pliki, drzewo jest czyste, dokument nie kłamie |

## 7. Definition of done (całość)

```sh
gofmt -l .                                   # puste
go build ./... && go vet ./...               # exit 0
go test -count=1 ./...                       # zielone
go test -race ./...                          # nowe: zielone
go test -count=1 ./internal/rooms/ -run Cancelled
```

Plus ręczne sprawdzenie AUD-001: README nie zawiera już `/v1` w sekcji Hermesa, a `docs/backend_hermes.md` i spec się zgadzają.

Nieobjęte tym planem (świadomie): AUD-010 (podpięcie `test-integration-openai` do CI wymaga usługi Ollama w workflow) i AUD-014/015/016/017 (kopie reguł i rozproszone timeouty — refaktor, nie poprawka).
