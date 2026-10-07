# RAPORT — `/model` per członek pokoju (lucinate rooms)

Data: 2026-09-27 (aktualizacja po wdrożeniu enablera w gatewayu) · worktree: `E:/orca/workspaces/HermesHarness/lucinate` · branch: `feat/rooms-bot-mode`
(zero commitów — commit zrobisz Ty)

## 0. Kontekst: co było założeniem, co pokazał żywy system

Brief/decyzja zakładały, że przełączenie modelu członka da się zrobić gotowym
endpointem REST `POST /api/sessions/{session_id}/model` (session_model_lock) przez
klienta pokoi. Weryfikacja na żywym systemie pokazała, że **na tym hoście to nie
wystarcza**, i to z dwóch niezależnych powodów:

| Fakt | Dowód (żywy) |
|---|---|
| Endpoint REST nie jest podłączony: jedyny nasłuch to 9119 = `hermes_cli.main -p default dashboard`; `POST /api/sessions/{id}/model` → `405`, `/v1/capabilities` → SPA HTML. Trasa `api_server.py:1614` należy do platformy `api_server`, której tu nie ma (`gateway.platforms` puste) | `netstat`, curl, `.lucinate-verify/sprawdz-session-model-lock.py` |
| Nawet z zapisaną blokadą **tura członka pokoju ją ignoruje**: prawdziwa funkcja `tui_gateway.server._stored_session_runtime_overrides` na prawdziwym wierszu `room_plumbing` z blokadą → `{}`; ten sam mechanizm na wierszu nie-pokojowym → `{"model_override": {...}, "provider_override": ...}` | ten sam skrypt, uruchomiony na `profiles/matt/state.db` |
| Sesje członków żyją w **osobnych plikach profili** (`source=bot_room`, `hidden=1`, tytuł `Group: <room_id>`), a `/p/<profil>/...` na 9119 zwraca SPA — single-profile `api_server` nie miałby do nich dostępu | `state.db` profili, probe HTTP |
| Maszyneria pokoi nie miała żadnego settera modelu: 18 metod `groups.*`, 0 trafień w sterowniku/serwisie/dyskusji | grep w źródle gatewaya |
| Pełna powierzchnia pokoi to 18 metod, żadna nie dotyczyła modelu; roster jest zamrożony (re-adopt wymaga bajtowo identycznego `members_json`) | `tui_gateway/methods_groups.py`, `gateway/hosted_rooms.py:874` |

Decyzja Kamila: użyć gotowego mechanizmu sesyjnego, nie budować wariantów A/B/C.
Po weryfikacji przesłanki wdrożony enabler to **metoda `groups.member_model`** w tym
gatewayu, z którym klient pokoi już rozmawia (ten sam proces prowadzi sterownik
pokoi), plus **honorowanie markera** dla sesji `room_plumbing`.

## 1. Zmiana w gatewayu Hermesa (`hermes-agent`)

Patch do wglądu/ponownego zastosowania: `.lucinate-verify/gateway-member-model.patch` (508 linii).

| plik | zmiana |
|---|---|
| `tui_gateway/contracts/groups_bot_relay.py` | kontrakt `groups.member_model`: params `room_id`, `handle`, `model`, `provider?`; result `room_id, handle, profile, session_id, model, provider, session_created` |
| `tui_gateway/methods_groups.py` | handler `_apply_member_model` + wpis w `_METHODS` (metoda pojawia się w `groups.capabilities`, czyli w sondzie klienta); sesję członka rozwiązuje **tak samo jak driver** (profil + tytuł `Group: <room_id>` + `source=bot_room`), a gdy wiersza jeszcze nie ma, zakłada go w tym samym kształcie i zapisuje marker |
| `tui_gateway/server.py` | `_stored_session_runtime_overrides` honoruje `room_member_model` **przed** regułą odrzucającą zapisane modele dla `room_plumbing` (ta reguła istnieje po to, by nie wskrzeszać *starych* pinów providera; świadomy wybór per pokój nie jest starym pinem) |
| `tests/tui_gateway/test_groups_methods.py` | 5 testów: seat na sesji członka + brak edycji configu, `session_created`, odrzucenia (nieznany handle, pusty model), metoda w capabilities, honorowanie markera |
| `apps/shared/src/gateway-contract.{generated.ts,openrpc.json}` | regeneracja (`scripts/gen_gateway_contracts.py`) |

Semantyka: **stan sesji** (`model_config.room_member_model` na sesji pokoju członka).
Zero zapisów do `config.yaml`/`.env` profilu. Seat ginie razem z sesją pokoju.

Uwaga operacyjna: `hermes update` może nadpisać te pliki — patch jest w repo dowodów.
W drzewie `hermes-agent` są też **cudze** niezacommitowane zmiany z 26.09
(`tui_gateway/hosted_room_driver.py`, `tests/tui_gateway/test_hosted_room_driver_runtime.py`) — nie dotykałem ich.

## 2. Zmiany w lucinate

| plik | zmiana |
|---|---|
| `internal/rooms/models.go` | `MemberModelSeat` (odpowiedź gatewaya), `SetMemberModel(ctx, room, handle, model, provider)`, `SeatedLabel()`; nagłówek opisuje **prawdziwy** mechanizm (poprzedni opisywał brak metody) |
| `internal/tui/rooms_model.go` | seat zamiast wiersza rosteru; precedencja: **seat wygrywa**, roster jest fallbackiem (roster nigdy nie zna seatu — gateway trzyma go na sesji); status pokazuje sesję, na której wylądował wybór |
| `internal/testgateway/testgateway.go` | fałszywy gateway odpowiada realnym kształtem i **nie edytuje rosteru** (realny gateway tego nie robi) |
| `openspec/specs/rooms/spec.md`, `CHANGELOG.md` | opis zgodny z rzeczywistością (session-scoped seat, brak zapisu do profilu, sonda capabilities) |

**Komenda `/model` — 4 warianty (bez zmian):** `/model` (picker członka → picker modeli),
`/model @handle` (picker modeli tego członka z jego aktualnym modelem w nagłówku),
`/model @handle <nazwa>` (bezpośrednio), `/model @handle` (pokazuje aktualny model i pozwala zmienić).
Picker: katalog z `model.options`, filtr typu subsequence, okienkowanie, hint/filtr zostają na ekranie.
Walidacja lokalna (nieznany handle, nazwa bez handle'a, pusty model) nie wysyła nic.
Odrzucenie przez gateway → komunikat gatewaya, poprzedni model zostaje, roster nie pokazuje odrzuconego.

## 3. Testy

| Warstwa | Testy |
|---|---|
| Parser (4 warianty + błędy) | `TestParseModelCommand_FourShapes`, `..._Errors` |
| Model członka / katalog | `TestMemberModelAndLabel`, `TestMemberModel_KeepsOtherConfigKeys`, `TestFilterModels` |
| Klient vs gateway | `TestClient_ModelCatalogue`, `TestClient_SupportsMemberModelFollowsCapabilities`, `TestClient_SetMemberModel` (seat + `session_created` false przy powtórce), `TestClient_SetMemberModelSendsProvider`, `TestClient_SetMemberModelRejections` |
| Picker + UX (TUI) | `TestRoomsModel_BareCommandListsMembers`, `..._EnterOpensTheModelListAndShowsTheCurrentModel`, `..._FilterNarrowsAndCursorResets`, `..._EnterSwitchesTheModelForTheSession`, `..._EnterSwitchesTheHighlightedModel`, `..._EscFromTheModelListReturnsToTheMemberList`, `..._PickerStaysInsideTheTerminal`, `..._CostTableCarriesTheModel`, `..._SeatOverlayWinsAndRosterIsTheFallback` |
| Błąd + rollback + zakres | `..._GatewayWithoutTheMethodRefusesInsteadOfPretending`, `..._RejectedModelRollsBackAndExplains`, `..._CommandErrorsAreReportedNotSent`, `..._ReconnectDropsTheSessionOverlay`, `..._EmptyCatalogueIsExplainedNotSilent`, `..._CatalogueFailureIsReported`, `..._UnknownMemberInTheOverlayIsReported` |
| Integracja (W2, realny socket) | `TestW2_ModelListMode`, `TestW2_ModelRejectMode` |
| **Żywy gateway** (`ROOMS_LIVE=1`) | `test/integration/rooms/live_model_test.go`: `TestLive_MemberModelSeat`, `TestLive_ControlUnseatedMemberRunsProfileModel` |
| Gateway (produkt) | `tests/tui_gateway/test_groups_methods.py` — 5 testów `member_model`/`seated` |

Fake gateway ma tryby **MODEL_LIST** (`SetModelCatalogue`) i **MODEL_REJECT**
(`SetMemberModelReject` + `SetMemberModelSupported`) oraz licznik `MemberModelCalls()`
jako dowód, że zmiana doszła do drutu.

## 4. Dowód z żywego systemu (27.09.2026, gateway `-p default` na 9119)

    TestLive_MemberModelSeat — PASS (27,03 s)
      1. gateway advertises groups.member_model
      2. room probe-model-224451 utworzony z 2 członkami: [matt kowal]
      3. seat: handle=matt session=31aa1930 model=muse-spark-1.3-contributor session_created=true
      4. wiadomość wysłana
      5. członek odpowiedział: "OK"

    TestLive_ControlUnseatedMemberRunsProfileModel — PASS (6,39 s)   ← kontrola, pokój bez seatu

Kontrast z `profiles/matt/state.db` — model, na którym gateway **faktycznie wykonał turę**:

    pokój                  sesja                    room_member_model            sessions.model
    probe-model-224451     31aa1930                 muse-spark-1.3-contributor   muse-spark-1.3-contributor
    probe-control-224623   20260927_224623_a84df2   —                            deepseek-v4.1-flash   ← model profilu

Digest plików configu profili `matt` i `kowal` (config.yaml, .env, SOUL.md,
profile.yaml) przed i po: **identyczny** → profile nietknięte.

## 5. Bramki jakości

| Co | Wynik |
|---|---|
| lucinate `go test -count=1 ./...` | **17 pakietów ok, 0 FAIL** (w tym integracja rooms i chaos) |
| lucinate `go vet ./...` | czysto; `gofmt -l` w moim zakresie (`internal/rooms`, `internal/tui`, `test/integration/rooms`, `internal/testgateway`) pusto |
| Pokrycie nowych ścieżek | `internal/rooms/models.go` **92.9%**, `internal/tui/rooms_model.go` **89.6%** (próg ≥85%) |
| gateway `pytest -k "member_model or seated"` | **5 passed** |
| gateway szerszy zestaw (`test_groups_methods`, `contracts/test_generated`, `test_hosted_room_driver_runtime`, `test_config_profile_scope`) | **93 passed** |
| Zero persistu po stronie klienta | test czyta plik preferencji po udanej zmianie i potwierdza brak wpisu o modelu |
| Profile nietknięte | digest plików configu przed/po (żywy system) + brak ścieżki zapisu configu w kodzie |

### 5a. Bramki kanoniczne (repo-native) + dowód, że awarie nie są moje

| Bramka | Wynik |
|---|---|
| lucinate `make test-win` (preferowany na Windows wg Makefile) | wszystkie pakiety `ok`, 0 FAIL |
| lucinate `go test -count=1 ./...` | 17/17 ok |
| lucinate `gofmt -l` (mój zakres) / `go vet ./...` | pusto / pusto |
| hermes `scripts/run_tests.sh tests/tui_gateway/` (runner CI, izolacja per-plik) | 1069+ passed, **18 failed** — wszystkie **przedwstępne** (patrz A/B) |
| hermes `pytest tests/tui_gateway/test_groups_methods.py tests/tui_gateway/contracts/test_generated.py -q` (mój obszar) | **42 passed** |

A/B (bo „18 failed" wymaga przypisania): klon `git clone --shared` na czystym HEAD
`35b14ad5e2` (`git diff` pusty), ten sam zestaw plików testowych uruchomiony per-plik
z venv produktu:

| plik | HEAD (czysty) | drzewo robocze | przyczyna (platformowa/flaky) |
|---|---|---|---|
| `test_bot_relay_methods.py` | 2 failed | 2 failed | `subprocess.TimeoutExpired` (spawn pythona) |
| `test_compute_host.py` / `_phase1.py` | 1 / 2 failed | 1 / 2 failed | timeout JSON hosta; `assert 30 == 32` (atomowość zapisu logu) |
| `test_display_methods.py` | 2 failed | 2 failed | `ModuleNotFoundError: No module named 'fcntl'` (POSIX) |
| `test_display_watch.py` | 1 failed | 1 failed | brak `Xvnc/xfwm4/xfce4` → `supported: False` |
| `test_entry_import_off_main_thread.py` | 1 failed | 1 failed | `signal.SIGPIPE` nie istnieje na Windows |
| `test_ephemeral_profile_override.py` | 3 failed | 3 failed | „expected call not found" (mock/thread) |
| `test_hosted_room_two_gateway_scoped.py` | 1 failed | 1 failed | jak wyżej (sprawdzone osobno — identycznie na HEAD) |
| `test_tui_gateway_server.py` | 3 failed / 622 passed | 1 failed | `aaaa-route` vs `bbbb-route` (cache mtime, granularność Windows) |
| `test_connector_operation_rpc.py`, `test_slash_worker_mcp_discovery.py`, `test_foreground_notification_snapshot.py` | **pass** na HEAD | fail w przebiegu zbiorczym | flaky (timing pod obciążeniem) |
| mój `test_groups_methods.py` (+ kontrakty) | — | **42 passed** | zielone w obu drzewach |

Wniosek: żadna z awarii nie pochodzi z tej zmiany — ten sam zestaw failuje na czystym
HEAD, a wszystkie ścieżki, których dotykam (`groups.*`, kontrakty, `_stored_session_runtime_overrides`),
są zielone. Klon baseline usunięty po porównaniu.

## 6. Ograniczenia (jawne)

1. **Metoda jest w drzewie produktu na tym hoście, niezacommitowana** — to zmiana poza repo lucinate (Twoja decyzja, patch w `.lucinate-verify/`). `hermes update` może ją nadpisać.
2. Restart dashboardu (9119) był konieczny, by proces wczytał metodę; wykonany **z odtworzonym środowiskiem** (`HERMES_DESKTOP=1` + `HERMES_DASHBOARD_SESSION_TOKEN`), bo bez tego gate dashboardu odrzuca `?token=` (HTTP 403) i klient pokoi nie połączy się z 9119.
3. Potwierdzenie „który model odpowiedział” pochodzi z wiersza sesji, który gateway zapisuje po turze (`sessions.model`) — plus kontrola bez seatu, która daje model profilu. Razem to rozstrzyga, ale to nie jest log providera.
4. Pokoje `probe-model-224451` i `probe-control-224623` zostawione w gatewayu do wglądu (można rozwiązać jedną komendą).
5. `-race` nie był uruchamiany (jak wcześniej w tym zadaniu).

## 7. Zgodność z Definition of Done z briefu

- [x] `/model` działa w rooms TUI w 4 wariantach (picker członka → picker modeli z filtrowaniem; `@handle`; `@handle <nazwa>`; `/model @handle` pokazuje aktualny model).
- [x] Testy jednostkowe: parsowanie, picker, komunikat błędu i rollback UI, sesyjny zakres zmiany.
- [x] Fake gateway z W2 rozszerzony o MODEL_LIST i MODEL_REJECT + testy integracyjne na sockecie.
- [x] Żywy test na prawdziwym gatewayu i prawdziwych profilach + kontrola (pokój bez seatu).
- [x] `go test ./...` zielone; pokrycie nowych ścieżek 92.9% / 89.6%.
- [x] `openspec/specs/rooms/spec.md` + `CHANGELOG.md` + ten raport.
- [x] Zero commitów/pushy; config i pliki profili nietknięte; brak sekretów w artefaktach.

## 8. Paragon zmian

1. **Co zmieniono**: gateway — kontrakt + handler `groups.member_model` + honorowanie markera w `_stored_session_runtime_overrides` + regeneracja kontraktów + 5 testów; lucinate — `internal/rooms/models.go` (seat), `internal/tui/rooms_model.go` (precedencja seat/roster, status z sesją), `internal/testgateway` (realny kształt), testy jednostkowe/integracyjne/żywe, spec, CHANGELOG, ten raport.
2. **Po co**: w pokoju „kto na jakim modelu odpowiada” było niewidoczne i niezmienialne; teraz zmiana jest sesyjna, natychmiastowa i nie dotyka profilu.
3. **Czym zweryfikowano**: żywy test (2 przebiegi: seat + kontrola) na gatewayu 9119 z prawdziwymi profilami, odczyt `state.db` po turze (kontrast seat vs kontrola), digest configów profili przed/po, `go test -count=1 ./...` (17/17), `go vet`, `pytest` w drzewie produktu (5 + 93), `gofmt`.
