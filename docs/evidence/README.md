# Evidence

`captures/` holds raw RDM capture logs taken on real rigs, committed verbatim.

`analysis/` holds the written investigations of those captures and the scripts
that produced them. `analyze_era.py` reads from `../captures/` and is
re-runnable: `python3 docs/evidence/analysis/analyze_era.py 34`.

They are committed because analysis summaries have twice turned out to be wrong
in ways only the raw bytes could settle — an assumed device count that was the
gateway's claim rather than the rig's truth, and an elapsed-time discriminator
that looked airtight until two ports contradicted it. An agent should be able to
re-derive a finding rather than trust a summary.

They contain real UIDs, DMX addresses and node IPs from client rigs. This repo
is private and must stay that way.
