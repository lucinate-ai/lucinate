# RAPORT — testy wyczerpujące lucinate rooms: warstwy W1, W2, W4

Data: 2026-09-27 · worktree: `E:/orca/workspaces/HermesHarness/lucinate` · branch: `feat/rooms-bot-mode`
(praca na bazie commita `f784363`; **zero commitów** — commit zrobisz Ty)

## 1. Co testowano

### W1 — jednostkowe, brzegowe

| Punkt z briefu | Test | Ustalone zachowanie |
|---|---|---|
| a) wzmianka w środku zdania | `TestW1_MentionGrammarEdgeCases/mid-sentence` | `hej @matt sprawdz to` → adresowany `matt`, tekst niezmieniony |
| a) dwa różne @handly | `TestW1_TwoHandlesAddressBothMembers` | **adresowane OBA** (w kolejności zapisu), tekst niezmieniony, tryb pokoju NIE jest stosowany, kursor round-robin nie rusza się |
| a) nieistniejący członek | `TestW1_TypoAndUnknownHandlesAreReportedNotSwallowed` | leci jak napisane + status „unknown handle”, **nigdy** nie rozszerza się na cały roster |
| a) literówka (`@mat`) | jw. | to samo co wyżej — brak cichego dopasowania „po prefiksie” |
| a) @handle jako część słowa (`@mattxyz`) | jw. | parsowany handle to `mattxyz` → nieznany, NIE trafia do `matt` |
| a) wiadomość z samym @handle | `TestW1_MessageThatIsOnlyAMentionStillRoutes` | routuje normalnie, wysyłane dosłownie `@matt` |
| a) unicode w handle | `TestW1_DerivedHandlesAreNeverATruncatedUnicodePrefix` | `@młody` → gramatyka gatewaya ucina do `m` → zgłoszone jako nieznany handle; `HandleFor("młody")` = `m-ody`, więc skrót NIE może trafić w złego członka (udokumentowane w spec) |
| b) przeplot delt dwóch agentów | `TestW1_InterleavedStreamsStayPerMember` | teksty się nie krzyżują; oba strumienie aktywne; po `turn.settled` jednego — drugi nadal leci |
| b) delta po finalu | `TestW1_DeltaAfterFinalDoesNotResurrectTheTurn` | tura nie wraca do stanu „w toku”, `ActiveStreams` puste |
| b) final bez delty | `TestW1_FinalWithoutAnyDeltaStillSettlesWithItsText` | tekst + settled, niepuste |
| b) pusta odpowiedź | `TestW1_EmptyReplyIsMarkedEmpty` + TUI | placeholder znika (nie ma spinnera na wieki) |
| c) N=0 | `.../zero_means_the_stored_default` | domyślne 6 |
| c) N = długość transkryptu | `.../window_equal_to_the_transcript_compacts_nothing` | nic nie kompaktowane |
| c) transkrypt krótszy niż N | `.../transcript_shorter_than_the_window_compacts_nothing` | nic, i **żądanie briefu nie leci do pokoju** |
| c) N ujemne | `.../negative_window_is_refused,_not_clamped` | **odrzucone z błędem** (`/compact -1` → error, zero kompaktu, zapisane okno bez zmian) |
| c) N ogromne | `.../a_huge_window_is_bounded` | ograniczone do 100 |
| d) pusta rozmowa | `TestW1_ExportOfAnEmptyConversationIsStillWellFormed` | markdown ma jawne `_no events_`/`_no members_`, JSON `null` (nie „cisza”) |
| d) polskie znaki + emoji | `TestW1_ExportPreservesPolishTextAndEmoji` | `Zażółć gęślą jaźń — 🚀 100% ✓` przechodzi bajt w bajt przez md i JSON, UTF-8 walidowany |
| d) determinizm JSON | `TestW1_ExportIsDeterministicForAFixedTimestamp` | dwa eksporty przy tym samym `Generated` są identyczne |
| e) backoff, serwer odmawia stale | `TestW1_BackoffCapsAndReportsItsState` + chaos | sufit trzyma (50 prób nigdy ponad `Max`), `Describe()` pokazuje numer próby i opóźnienie — **brak kręcenia się w ciszy**; `Reset()` wraca do bazy |

### W2 — integracyjne z fałszywym backendem

Fałszywy gateway: `internal/testgateway` (czysty Go: `net/http/httptest` + `gorilla/websocket`, zero nowych zależności) mówiący prawdziwym protokołem JSON-RPC po WS; sterowany parametrem (`Configure(Reply{...})`, `SetRejectSend`, `SetSilent`, `SetUpgradeStatus`, `CloseConns`).

| Tryb | Test | Co udowodniono |
|---|---|---|
| DROP_MID_STREAM | `TestW2_DropMidStreamResumesWithoutLoss` | fragmenty ocalały, martwy klient zwraca błąd zamiast wisieć, redial wznawia od kursora, **zero duplikatów seq**, pokój dalej przyjmuje wiadomości |
| SLOW_STREAM | `TestW2_SlowStreamIsObservablePartially` | odpowiedź widoczna **cząstkowo** w trakcie (drugie połączenie), fragmenty w kolejności, tura domknięta na końcu |
| REJECT | `TestW2_RejectedSendIsReportedAndTheRoomRecovers` | błąd 4001 z komunikatem gatewaya, socket żyje, pokój czytelny, kolejna wiadomość przechodzi |
| SLOW_RESPONSE | `TestW2_SlowResponseIsBoundedByTheCallTimeout` | brak odpowiedzi ograniczony timeoutem (300 ms → koniec w 0.30 s), połączenie dalej działa (odpowiednik `TestClient_CallTimeoutIsBounded` z `backend-hermes`) |
| MULTI_AGENT_ECHO | `TestW2_MultiAgentEchoKeepsRepliesWithTheirMember` | dwóch członków naprzemiennie, każdy tekst przypisany do swojego członka, brak przeplotu |

Profil z briefu (500 ms × 60 = 30 s) jest **opt-in**: `ROOMS_W2_LONG=1` → przebieg: `partial observation: 59 of 60 deltas visible mid-stream (interval 500ms)`, PASS w 29.6 s. Domyślny profil to 12 delt × 40 ms, żeby suita nie spała pół minuty.

### W4 — chaos / awarie

| Punkt | Test | Co udowodniono |
|---|---|---|
| a) kill procesu backendu w trakcie tury | `TestChaos_KilledBackendMidTurnKeepsFragmentsAndRecovers` | prawdziwy proces (`test/integration/chaos/gateway`) ubity w połowie odpowiedzi: 3 zdarzenia złapane przed ubićiem, tekst częściowy zachowany i **niedomknięty**, wywołanie do martwego backendu kończy się błędem, po restarcie + redialu pokój przyjmuje i odpowiada (nie wisi) |
| b) brak miejsca przy eksporcie | `TestChaos_UnwritableExportDirIsAnErrorNotAPanic` | katalog danych nie do utworzenia (składnik ścieżki jest plikiem — przenośny stand-in pełnego/read-only dysku): sensowny błąd, **zero paniki**, żadnego pliku nie zaraportowano, renderowanie eksportu nadal działa |
| c) rozmowa 100+ wiadomości | `TestRoomsW4_LongTranscriptStaysWindowedAndScrollable` (150 wiadomości, 3 agentów) | widok ograniczony wysokością terminala + licznik ukrytych linii, scroll do najstarszych działa, 20 renderów w budżecie czasu (bez OOM/degradacji) |
| d) dwa klienty w jednym pokoju | `TestChaos_TwoClientsShareOneRoomWithoutDiverging` | identyczny zbiór seq u obu, zero duplikatów w stronie, zero odmów 4xx, oba nadal nadają i czytają stan |

## 2. Co wykryto (i co z tym zrobiono)

**Naprawione przez ten task (kod produkcyjny):**

1. **Nota routingu ginęła** — `routeOutgoing` miał receiver wartościowy i zapisywał `m.status` w kopii, więc użytkownik nie dowiadywał się, gdzie poszła wiadomość bez @handle w trybie moderator/round-robin. Teraz nota jest zwracana i ustawiana przez `post()`. *(wykryte przez W1a)*
2. **Budżet wierszy nie liczył bannera połączenia** — otwarty pokój z ustawionym połączeniem renderował o 1 linię za dużo, wypychając kompozytor poza ekran. *(wykryte przez W4c)*
3. **Scroll na maksimum opróżniał panel** — clamp do liczby linii pozwalał oknu zwinąć się do `[0,0]`, więc `Home` w długim pokoju **kasował widok zamiast pokazać początek**. Clamp trzyma teraz jedno pełne okno historii. Istniejący test (`TestRoomsModel_ScrollIsClampedToTheHistory`) **utrwalał ten bug** — poprawiony z komentarzem wyjaśniającym. *(W4c)*
4. **`loaded` ustawiane nawet przy błędzie** — nieudany `groups.list` (np. brak połączenia z gatewayem) pokazywał „no rooms yet”, czyli kłamał o pustym koncie; widok mówi teraz „rooms could not be loaded”. *(W1b przez model)*
5. **Zero-value client panikował** — `Done()` na kliencie bez `rpc` i `WSURL()` na `nil *Client` rzucały panic; oba są nil-safe (ktoś trzymający slot w cache nie wywali TUI). *(W1e / pełne pokrycie klienta)*
6. **`/compact -N` było clampowane zamiast odrzucane** — ciche skompaktowanie z innym N niż podane kasuje historię, którą użytkownik chciał zachować. Dodane `ValidateKeepWindow` (0 = zapisane okno, ujemne = błąd). *(W1c)*
7. **Przerwana tura była niewidoczna** — brak `turn.settled` (bo tura umarła) zostawiał wiersz „w trakcie pisania” na zawsze. Dodane: snapshot tekstu częściowego, wiersz „⚠ reply interrupted”, stop spinnera, powrót pokoju do gotowości, kasowanie markera **tylko** po nowszym zdarzeniu tego członka (replay historii po reconnectcie go nie kasuje). *(W4a)*

**Poprawki w samych testach i higiena drzewa:**

- **Flaky asercja (znaleziona przy pełnym przebiegu)** — `TestClient_DroppedSocketSetsErrAndClosesDone` sprawdzał `<-c.Done()` nieblokującym `select` zaraz po `Err() != nil`; read loop ustawia `Err()` i dopiero potem zamyka `Done`, więc okno czasowe dawało sporadyczny FAIL tylko w pełnej suicie. Test czeka teraz na sygnał z budżetem czasu. Dwa kolejne przebiegi pakietu — zielone.
- **`make fmt` z fazy `hermes verify` przeformatował 3 pliki spoza zakresu** (`app/app.go`, `app/chat.go`, `internal/client/client.go` — wcześniejszy dryf gofmt, nie mój). Przywrócone do `HEAD`, żeby diff tego zadania został skupiony na testach i pokazanych wyżej naprawach. Dryf tych trzech plików jest wcześniejszy i nadal tam jest, gdy ktoś odpali `make fmt`.

**Odłożone (z powodem):**

- **Timeout pollu stanu jest cicho ignorowany** — świadoma decyzja: sygnałem pierwotnym jest poll logu (jego awaria uruchamia reconnect z widocznym komunikatem), a malowanie błędu przy każdym ticku byłoby szumem. Test `TestRoomsW1_StatePollIsBoundedAndRecovers` pilnuje, że poll **kończy się** w ograniczonym czasie i że widok wraca do pracy, gdy gateway znów odpowiada. Jeśli wolisz, by po N nieudanych pollach pojawiał się komunikat — to jedno miejsce (`case roomsStateMsg`).
- **`internal/testgateway` nie ma własnych testów** (0.0% w swoim przebiegu) — to test double; jego API jest pokrywane przez testy `internal/rooms`/`internal/tui`/`test/integration/*`. Nie dublowałem testów na atrapę.
- **Chaos-kill wymaga toolchainu Go** — test buduje binarkę gatewaya; bez `go` w `PATH`/`GOROOT` **skipuje z powodem** (nie „przechodzi po cichu”). Na tym hoście przeszedł: 1.00 s.

## 3. Ograniczenia warstw

- W3 (live macierz profili), W5 (soak), W6 (dogfood) — **poza zakresem** tego briefu; nie dotykałem profili Hermesa ani produkcyjnego gatewaya.
- `-race` odpuszczony (brak cgo) — zgodnie z briefem.
- Obserwacja strumienia „w trakcie” wymaga **drugiego połączenia**: serwer obsługuje jedno połączenie sekwencyjnie, więc poll po tym samym sockecie, który streamuje, czeka za odpowiedzią. To jest udokumentowane w README obu pakietów i wykorzystane w testach (SLOW_STREAM, kill).

## 4. Dowody

| Co | Komenda | Wynik |
|---|---|---|
| Cała suita | `go test -count=1 ./...` | **17 pakietów ok, 0 FAIL** (w tym nowe `internal/testgateway`, `test/integration/rooms`, `test/integration/chaos`) |
| Pokrycie rooms | `go test ./internal/rooms/ -cover` | **89.7%** (próg ≥88%, start 86.0%) |
| Pokrycie tui | `go test ./internal/tui/ -cover` | **75.2%** (próg ≥75%, start 73.3%) |
| Vet / format | `go vet ./...`, `gofmt -l internal test app` | czysto / pusto |
| Bramka projektu | `hermes verify --json` | **ok: true** — `go build ./...` (1.66 s), `go test ./...` (6.61 s), `make test`, `make build`, `make fmt` — wszystkie exit 0 |
| Profil z briefu | `ROOMS_W2_LONG=1 go test ./test/integration/rooms/ -run TestW2_SlowStream -v` | PASS 29.6 s, 59/60 delt widocznych w trakcie |
| Przeglądarka | raport HTML w Orca browser (goto → klik w zakładki → odczyt DOM) | 4 zakładki, KPI: `17/17 ok`, `89.7%`, `75.2%`, `3/3`, `29`, `ok=true`; 14 kroków dowodowych; `coverage: 89.7%`/`75.2%`; **0 wystąpień FAIL**; obecne dowody kill/long-profile/verify/chaos |

Surowe dowody: `.lucinate-verify/rooms-testy-tests.txt` (pełne logi), `.lucinate-verify/rooms-testy-evidence.json` (14 kroków), `.lucinate-verify/hermes-verify-testy.json`, raport HTML: `.lucinate-verify/rooms-testy-weryfikacja.html`.

## 5. Otwarte pytanie do Ciebie

Dwa zachowania zmieniłem „w duchu briefu”, ale to decyzje produktowe:
1. `/compact -N` → **błąd** (wcześniej: ciche domyślne okno).
2. Przerwana tura → **trwały wiersz „interrupted”** w transkrypcie (kasowany, gdy członek znów odpowie).

Jeśli wolisz inne zachowanie (np. automatyczne wznowienie przerwanej tury po reconnectcie), to zmiana w jednym miejscu (`markInterrupted` / `cmdCompact`).

## 6. Zgodność z Definition of Done

- [x] Wszystkie testy W1/W2/W4 przechodzą: `go test ./...` zielone, **w tym nowe pakiety** `chaos` i `integration/rooms`.
- [x] Pokrycie: **rooms 89.7% ≥ 88%**, **tui 75.2% ≥ 75%**.
- [x] `openspec/specs/rooms/spec.md` zaktualizowany: dwie wiadomości z handlami, `@mattxyz`, literówka, unicode; odrzucanie ujemnego N; nowe wymaganie *Interrupted replies* ze scenariuszami; sufit backoffu z komunikatem.
- [x] Raport: ten plik.
- [x] Zero commitów / pushy; bez dotykania innych worktree, profili Hermesa i baz danych; zero sekretów w kodzie, testach i raporcie (wszystkie adresy/tokeny w testach są lokalne i puste).
- [x] `-race` pominięty (brak cgo).

## 7. Paragon zmian

1. **Co zmieniono**: nowy pakiet `internal/testgateway` (fałszywy gateway z trybami awarii), testy `internal/rooms/edge_cases_test.go` + `client_full_test.go` (W1 + pełne pokrycie `rooms.Client`), `internal/tui/rooms_w1w4_test.go` (W1 przez model, przerwana tura, W4c, pokrycie widoku), `test/integration/rooms/` (W2), `test/integration/chaos/` (W4 + killowalna binarka + README), plus **7 napraw w kodzie produkcyjnym** i aktualizacja spec.
2. **Po co**: rooms v2 weszło bez warstw brzegowej/awaryjnej; bez nich „działa” znaczyło tylko „działa na szczęśliwej ścieżce”, a trzy realne bugi (nota routingu, budżet wierszy, scroll kasujący widok) były niewidoczne w testach.
3. **Czym zweryfikowano**: `go test -count=1 ./...` (17/17 ok), `-cover` rooms 89.7% / tui 75.2%, `hermes verify --json` ok=true, profil SLOW_STREAM z briefu (PASS 29.6 s), chaos z prawdziwym ubiciem procesu, oraz raport HTML sprawdzony w Orca browser (klikane zakładki, odczyt DOM, 0 FAIL).
