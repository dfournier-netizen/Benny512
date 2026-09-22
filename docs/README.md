# docs

| Path | What it is | Read it when |
| --- | --- | --- |
| `HANDOFF.md` | **Current state.** Overwritten in place; one authoritative file. | Starting any session. |
| `notes/YYYY-MM.md` | Append-only session and decision record, split by month. | You need the reasoning behind something the handoff only states. |
| `decisions/` | One file per durable decision. Greppable, cheap to read. | Before re-opening a settled question. |
| `reference/` | Research and architecture documents, hardware test checklists. | Working in the area each one covers. |
| `evidence/captures/` | Raw RDM capture logs from real rigs. | Diagnosing anything about discovery, RDM or a specific fixture. |

Stale handoffs are a hazard, which is why there is exactly one and it is
rewritten rather than dated. The month files are the history; git is the
history of the history.
