# DONE flota-research (5 profili, 2026-09-28, profil matt, praca wlasna)

Wszystkie 5 profili (scout, archivist, verifier, synthesizer, publisher) dostalo wzorzec minera: `config get fallback_providers` pokazuje `nous / xiaomi/mimo-v2.6-flash` (ustawione przez `hermes -p <profil> config set`, klucz dowidziony w pilocie — bez ponownego probowania), a kazdy SOUL.md zawiera sekcje `## Orientacja` (narzedzia > kontekst sesji + single tool_call), wstawiona przed linia Eskalacji profilu; 10 backupow `.bak-2026-09-28-matt2` istnieje i sa przed-zmianowe (diff backup-vs-live = dokladnie blok fallback + sekcja Orientacji, linie Eskalacji bajtowo identyczne — md5 match).

Zweryfikowane: get fallback na kazdym z 5 (nous/mimo), grep Orientacja live=1/bak=0 i fallback-w-baku=0 na wszystkich, smoke test `research-scout chat -q "odpowiedz jednym slowem: ok" -Q` → `ok` (sesja 20260928_233139_70894a), research-miner nietkniety (mtime config/SOUL z pilota 23:17/23:13), zero commitow, zero sekretow w logach.

Odlazone: nic — wpadka z archivista (fuzzy patch podmienil `"` na `”` w linii Eskalacji, wykryte diffem, naprawione pythonem + weryfikacja od/md5) to lekcja na przyszlosc: przy patchowaniu SOUL-i najpierW `od -c` linii granicznych, bo cudzyslowy polskie vs ASCII rozjezdzaja kryterium czystego diffa.
