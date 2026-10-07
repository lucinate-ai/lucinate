# DONE pilot-miner (research-miner, 2026-09-28, profil matt, praca wlasna)

Fallback: `hermes -p research-miner config get fallback_providers` pokazuje `nous / xiaomi/mimo-v2.6-flash` (inny niz domyslny opencode-go), ustawiony przez `config set` po backupie `config.yaml.bak-2026-09-28-matt`, a SOUL.md zawiera sekcje `## Orientacja — zanim odpowiem o miejsce i stan` z nakazem weryfikacji lokalizacji narzedziami (`orca worktree current/list`, `terminal list`) i zdaniem o pojedynczych wywolaniach tool_call (backup `SOUL.md.bak-2026-09-28-matt`).

Dowod dzialania: probe obu providerow one-shotami `chat -q "odpowiedz jednym slowem: ok" -Q` — primary opencode-go (`-m muse-spark-1.3`, sesja 20260928_231618_e9a035) i fallback nous (`-m xiaomi/mimo-v2.6-flash`, sesja 20260928_231632_f5d32b) odpowiedzialy `ok`, a normalna sesja domyslna po zmianie (20260928_231648_00f148) tez `ok`; pozostale 5 profili nietkniete (ich SOUL.md z 25.09, configi ruszane ostatnio o 16:48 przez runtime, nie przeze mnie), zero commitow, zero sekretow w logach.

Odlazone: wymuszony failover ze sztucznie zepsutym primary (brief dopuszcza zamiennik w postaci probe obu providerow, z czego skorzystalem) oraz rollout na scout/archivist/verifier/synthesizer/publisher — czeka na osobny brief flotowy.
