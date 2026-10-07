# RAPORT — rozbudowa lucinate rooms (lucinate-rooms-v2)

Data: 2026-09-27 · worktree: `E:/orca/workspaces/HermesHarness/lucinate` · branch: `feat/rooms-bot-mode` (praca niezacommitowana — zero commitów, zero pushy, zgodnie z granicami z briefu)

## 1. Co zrobione (6/6 funkcji z briefu)

### 1.1 Routing wypowiedzi
- `internal/rooms/routing.go` — parsowanie `@handle` **gramatyką gatewaya** (`@([A-Za-z0-9][A-Za-z0-9._:-]*)`), z odrzucaniem adresów e-mail (`kowal@example.com` to nie mention), dopasowanie do rosteru po handle ORAZ profilu, `Route()` dla trzech trybów.
- Tryby: `broadcast` (domyślnie, bez zmian względem dotychczasowego zachowania), `moderator`, `round-robin`. Routing jest server-side w protokole, więc tryb, który wybiera członka, **dopisuje jego mention do tekstu** — inaczej gateway nie miałby jak zaadresować wiadomości.
- Explicit `@handle` w treści zawsze wygrywa z trybem (użytkownik napisał go ręcznie).
- Kursor round-robin i moderator są persystowane per pokój w `rooms-prefs.json` (`internal/rooms/roomprefs.go`) — osobny plik od `rooms.json`, żeby zepsuty zapis preferencji nie skasował predefined rooms.
- TUI: `/mode [broadcast|moderator|round-robin]`, `/moderator [@handle]`, `/broadcast`, `/round-robin`; tryb pokazany w nagłówku transkryptu.

### 1.2 Streaming odpowiedzi w TUI rooms
- `internal/rooms/stream.go` — składanie logu w jeden strumień na członka: `turn.started`, delty (kumulatywne, z obsługą `append: true`), `message.member`, `turn.settled/failed/deferred`, `member.unavailable`; odpowiedź pusta oznaczana jako `Empty`.
- TUI: wiersz live per członek z braille spinnerem **120 ms** (ten sam cadence co chat), tekst rośnie w miarę jak przychodzi; **placeholder znika w momencie zakończenia tury, także gdy odpowiedź jest pusta**.
- Akceptowane są dwa kształty delt: dedykowany kind `message.member.delta` oraz `message.member` z flagą `partial`/`streaming` w payloadzie.

### 1.3 Reconnect / resume pokoju
- `internal/rooms/backoff.go` — exponential backoff 500 ms × 2, sufit 30 s, `Reset()` po udanym połączeniu; `IsDisconnect()` klasyfikuje błędy transportu (a NIE błędy JSON-RPC ani świadomy `Close`).
- `rooms.Client.Err()` — rozróżnia zerwany socket od własnego teardownu (po `Close()` `Err()` jest nil).
- TUI: po zerwaniu historia na ekranie **zostaje**, status pokazuje redial, po udanym redialu log jest dociągany od trzymanego kursora (`since`) — bez utraty i bez duplikatów; stary tick rediala jest ignorowany, gdy nowsza próba go zastąpiła.

### 1.4 Eksport transcriptu
- `internal/rooms/export.go` — markdown (tytuł, room id, czas UTC, roster, zdarzenia z mówcą) i JSON (room + roster + surowe eventy); zapis do `<lucinate data dir>/exports`, plik 0600 w katalogu 0700.
- TUI: `/export [md|json|both]`; status pokazuje ścieżki, notice je wypisuje.
- Czas w eksporcie jest **UTC** — dokument czyta się na maszynach w innych strefach.

### 1.5 Kompakt długich rozmów
- `internal/rooms/compact.go` — podział transkryptu na „starszą część” i ostatnie N wiadomości (`N` default 6, sufit 100), prompt do rosteru, deterministyczny brief lokalny, `ApplyCompaction`.
- TUI: `/compact [N]` wysyła do pokoju prośbę o brief (routowaną trybem pokoju) i zapisuje pierwszą odpowiedź członka po niej jako brief; `/compact local [N]` tworzy brief lokalnie (działa bez osiągalnego rosteru i nie kosztuje tury).
- Brief zajmuje miejsce streszczonych wiadomości (razem z ukrytym żądaniem `/compact`), ostatnie N widać w pełnym brzmieniu; kompakt jest persystowany per pokój.
- Log gatewaya jest append-only i współdzielony — kompakt jest **widokiem klienta**, nie mutacją historii.

### 1.6 UX: kolory, koszt, szukanie
- `internal/rooms/colors.go` — paleta + deterministyczny wybór po hashu handle (kolor nie zmienia się między sesjami), `NormalizeHex` waliduje i normalizuje; `/header @handle #RRGGBB` i `/header @handle default`, persystencja per pokój.
- `internal/rooms/usage.go` — ekstrakcja usage z tolerancją kształtów (`usage`/`tokens`, `input_tokens`/`prompt_tokens`/`in`, koszt gdy jest), agregacja per członek (tury, wiadomości, tokeny, koszt), `FormatUsage` jako tabela; `/cost` mówi wprost, gdy gateway nie raportuje tokenów albo nie podaje kosztu.
- `internal/rooms/search.go` — `Find` (case-insensitive, snippet ±40 znaków, kolejność po Seq/Index), `/find <fraza>` listuje trafienia i **podświetla** dopasowania w transkrypcie; `/find` bez frazy czyści.

## 2. Dowody

| Co | Komenda | Wynik |
|---|---|---|
| Testy całego repo | `go test -count=1 ./...` | **16/16 ok, zero FAIL** |
| Pokrycie pakietu rooms | `go test ./internal/rooms/ -cover` | **86.0%** (baseline 75.9%; próg z briefu ≥70%) |
| Pokrycie tui | `go test ./internal/tui/ -cover` | **73.3%** (baseline 72.2%) |
| `go vet ./...` | | brak uwag |
| `gofmt -l internal app` | | 3 pliki z **wcześniejszym** dryfem (`internal/client/client.go`, `app/app.go`, `app/chat.go`) — nietknięte |
| Live pass po prawdziwym WS | `go test ./internal/tui/ -run TestRoomsV2Live -v` | **PASS, 14 kroków** |

Live test (`internal/tui/rooms_live_test.go`) uruchamia serwer WebSocket w procesie, łączy się z nim **produkcyjnym `rooms.Client`** i przepuszcza realne widoki TUI przez 14 kroków:
1. dial + `groups.log`, 2. routing round-robin **na drucie** (`groups.send` payload = `@matt co robimy?`), 3. delty + animacja spinnera (⠋ → ⠙), 4. pusta odpowiedź usuwa placeholder, 5. finalna wiadomość zastępuje wiersz live, 6. `/find`, 7. `/cost` (1500 tokenów, $0.0123), 8. `/header` + zapis do `rooms-prefs.json`, 9. `/export both` (dwa pliki), 10. zerwany socket (historia 8 eventów zachowana, backoff attempt 1), 11. odmówiony redial (attempt 2), 12. resume `reconnected — history resumed from seq 8` bez duplikatów, 13. `/compact local 4`, 14. `/mode`.

Zrzuty z tego przebiegu: `.lucinate-verify/rooms-v2-evidence.json` (14 kroków z renderami widoków) → raport HTML `.lucinate-verify/rooms-v2-verification.html` (sprawdzony w Orca browser).

### 2.1 Weryfikacja w Orca browser (na tym worktree)

Raport HTML otwarty w bocie Orca worktree `HermesHarness` i sprawdzony jak użytkownik (goto → klik w zakładkę → odczyt DOM):

1. `goto file:///E:/orca/workspaces/HermesHarness/.lucinate-verify/rooms-v2-verification.html` → tytuł strony `lucinate rooms v2 — raport weryfikacji`, sekcja aktywna `overview`, **6 kart KPI**: `16/16 ok`, `86.0%`, `73.3%`, `6/6`, `14`, `104 / 711`, 11 pozycji artefaktów, 6 punktów zakresu.
2. Klik w zakładkę „Dowody live” → sekcja aktywna `live`, **14 kart kroków**, każda z realnym renderem widoku TUI (12–24 linii). W treści widoczne m.in.: payload na drucie `@matt co robimy?`, animowane glify spinnera `⠋` i `⠙`, tabela `/cost` (1500 tokenów, `$0.0123`), `reconnected — history resumed from seq 8` (dials=3, events=8, bez duplikatów), `/compact local 4` (`events=32 → brief + last 4 messages visible (seqs > 28)`), ścieżki plików z `/export`.
3. Klik w zakładkę „Testy i pokrycie” → sekcja aktywna `tests`, w treści `coverage: 86.0%` i `coverage: 73.3%`, **zero wystąpień `FAIL`**, obecne sekcje `go vet` i `gofmt`.

(A11y `snapshot` Orca zwracał `runtime_unavailable` na tym dokumencie — dwie próby, po czym przeszedłem na `click` + `eval`, co daje ten sam dowód: realne kliknięcia w zakładki i odczyt wyrenderowanego DOM.)

## 3. Artefakty

- Kod: `internal/rooms/{routing,roomprefs,backoff,stream,compact,export,colors,usage,search}.go` + testy jednostkowe (osobny plik `_test.go` na każdy moduł),
- TUI: `internal/tui/rooms_ux.go` (nowy — streaming, reconnect, komendy v2), `internal/tui/rooms.go` (rozszerzony), testy `internal/tui/rooms_v2_test.go` (19 testów) i `internal/tui/rooms_live_test.go` (pass live),
- Specyfikacja: `openspec/specs/rooms/spec.md` (nowa — 9 wymagań, każde ze scenariuszami GIVEN/WHEN/THEN), `openspec/changes/2026-09-27-rooms-v2/{proposal,tasks}.md`,
- `CHANGELOG.md` — sekcja `[Unreleased]`.

## 4. Co odłożone / znane ograniczenia

1. **Słownik delt gatewaya nie jest udokumentowany w tym repo.** Klient przyjmuje dwa kształty (dedykowany kind + flaga w payloadzie). Gateway, który nie emituje żadnego z nich, degraduje się do renderowania tylko z pollingu: wiersze „is thinking”/tura nadal działają, ale tekst nie rośnie przyrostowo. Do potwierdzenia na żywym gatewayu (poza granicami tego zadania — nie dotykałem profili ani produkcyjnego gatewaya).
2. **`/compact` (brief z rosteru) kosztuje jedną turę** — wysyła do pokoju prośbę, którą roster odpowiada. Dlatego istnieje `/compact local [N]` (bez rosteru, bez kosztu). W live-passie przećwiczony został wariant `local`; ścieżka briefu z rosteru (pendingCompact → applyBrief) jest pokryta testem modelu `TestRoomsV2_CompactionHidesOlderMessagesAndKeepsTheTail`.
3. **Kompakt jest widokiem klienta** — inny klient tego samego pokoju nadal widzi pełny transkrypt. To świadoma decyzja: log gatewaya jest append-only i współdzielony.
4. **`/cost` pokazuje koszt tylko wtedy, gdy gateway go podaje**; inaczej tokeny bez kolumny kosztu i wyraźny komunikat.
5. **Eksport JSON jest compact (bez indentacji)** — taki kontrakt mają testy eksportu; czytelny dla człowieka jest markdown.
6. **`/export` w CLI** (`lucinate rooms export`) nie został dodany — brief wymagał działania w TUI, a powierzchnia CLI dla eksportu to naturalny, ale osobny krok.
7. **Konflikt konwencji**: `openspec/config.yaml` mówi „Never edit CHANGELOG.md by hand — the release process owns it”, a brief wymaga wpisu w CHANGELOG. Dodałem sekcję `[Unreleased]` (bez wersji i daty wydania) i flaguję rozbieżność do decyzji.
8. **Uwagi procesowe**: pracę rozbiłem na 3 równoległe workstreamy (eksport, kolory+szukanie, usage). Kolory i szukanie zostały dostarczone (ich pliki działają i są pokryte testami); eksport utknął na poprawce `ExportMarkdown` (bez errora) i został zatrzymany w połowie, a workstream od `usage` zaraportował sukces, ale na dysku nie było jego plików. **Przyczyna zniknięcia `export.go` z drzewa**: drugi agent, żeby przetestować własne pliki, tymczasowo **przeniósł cudzy `export.go` poza pakiet** („potem przywracam”) — i w tym stanie został zatrzymany. To potwierdza regułę: nie pozwalaj dzieciom pracować w tej samej kompilowanej paczce co Ty; niedokończony cudzy plik blokuje build wszystkim. Dokończyłem eksport i usage sam, na kontrakcie testowym dzieci — z dwiema poprawkami w ich oczekiwaniach: znacznik czasu w eksporcie liczony w UTC (ich `15:00:00` nie odpowiadał żadnej strefie) oraz asercje uprawnień POSIX przez `requirePosixFileModes` (na Windows `os.Chmod` nie ustawia trybów POSIX, więc taki test pada zawsze). Drzewo po zatrzymaniu agentów sprawdzone: brak plików-śmieci (`*.bak`/`*.orig`/odsuniętych kopii), żaden plik `.go` poza `internal/rooms` i `internal/tui` nie został tknięty.

## 5. Zgodność z Definition of Done z briefu

- [x] Każda z 6 funkcji działa w TUI i ma testy jednostkowe (routing parse, backoff, format eksportu, historia po compact, kolory, search — plus streaming i reconnect na poziomie modelu i na prawdziwym sockecie).
- [x] Pokrycie pakietu rooms **86.0%** ≥ 70%.
- [x] `go test ./...` zielone (zero FAIL).
- [x] `CHANGELOG.md` + `openspec/specs/rooms/spec.md` (spec utworzony od zera; brakujący wcześniej).
- [x] Raport: ten plik.
- [x] Zero commitów / pushy; nie dotykałem innych worktree, profili Hermes ani baz danych; brak sekretów w kodzie, testach i raporcie.

## 6. Paragon zmian (3 linijki)

1. **Co zmieniono**: 9 nowych plików w `internal/rooms` (routing, preferencje pokoju, backoff, streaming, kompakt, eksport, kolory, usage, search) + nowy `internal/tui/rooms_ux.go` i rozszerzony `internal/tui/rooms.go` (6 komend i wiersze live), do tego spec `openspec/specs/rooms/spec.md`, wpis w CHANGELOG i ten raport.
2. **Po co**: jeden pokój z 6 profilami odpowiadał całością na każdą wiadomość, transkrypt nie miał streamingu, wyszukiwania, eksportu ani kompaktu, a zerwanie socketu zabierało kontekst widoku.
3. **Czym zweryfikowano**: `go test -count=1 ./...` (16/16 ok), `-cover` rooms 86.0% / tui 73.3%, `go vet` czysty, oraz live pass `TestRoomsV2Live` na prawdziwym WebSocket (14 kroków, dowody w `.lucinate-verify/rooms-v2-evidence.json` i raporcie HTML zweryfikowanym w Orca browser).
