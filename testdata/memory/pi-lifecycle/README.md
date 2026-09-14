# Pi memory acquisition and correction

These original scenarios exercise the normal Pi memory lifecycle with real
model-selected `remember`, `recall`, `show`, and `link` calls. They complement
the long-horizon fixtures, which seed complete source turns to isolate retrieval
and answering from acquisition.

`native-acquisition` starts with an empty store. Pi must save the explicitly
requested AsterGate preferences before its answer; a fresh session must recover
the PostgreSQL database and Tuesday 09:00 UTC deployment window from durable
memory. Inspect the tool results and final stored records, not just a claim
that the facts were saved.

`history-update` begins with a dated Lyon residence. Pi receives a later move
to Porto and then answers both a historical and a current question in a fresh
session. Both facts must remain active records, with an explicit `supersedes`
edge from the new residence to the old one. The retained Ember Studio fact
must not be changed. A missing old fact, reverse edge, or unsupported answer
fails acceptance. The seeded history is not an answer to the update request.

These are development acceptance scenarios, not a held-out benchmark. The
runner preserves the actual tool transcript and an independent final SQLite
snapshot for inspection. A provider error or incomplete turn leaves the live
acceptance unresolved; it is not an acquisition failure score.
