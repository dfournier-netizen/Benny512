# Package refresh — 2026-10-09

Decision source: [owner addendum through I2f](decisions-after-handoff.md). This refresh changes design artifacts only. Production implementation remains on its own branch; C9 is separate.

| Status | Contract |
|---|---|
| Owner confirmed / shipped | One-shot commands: 1 s output, 0.75 s gesture for other Control functions, Reset/Lamp off non-modal confirmation, no disarmed queue, shared progress |
| Owner confirmed / shipped | Per-cell Lowlight; persistent Locate; virtual dimmers beneath masters; retained Store Control preset |
| Owner confirmed / shipped in I2f | Tests open tested-cell masters; unset colour is white for dimmer tests only; per-channel masked-test explanation; transient Armed-only Isolate |
| Shipped composition | Phone count/Clear/More above grid, Highlight ON below count; Tests sheet; horizontal banks; all raw slot labels not reported; per-step sequence interval; no running edits or drag |
| Shipped station controls | Single browser-local Readout default %, MIDI link glyph/word, Encoder n and Fine |
| Implementation fill-in | Group bank opens by remembered browser choice or ≥1300×1080; collapse never releases |
| Deferred proposals | Universe chips; fixture P/T marks; Live preview toggle; parameter TEST/override source tags; per-control units; vertical banks |
| C9 preparation | Light/custom themes and theme validation; current production is dark only |
| Open I2f follow-ups | Legacy test presets can still load old Isolate; white claims release immediately rather than following the test fade; raw/Identify changes refresh masked notes on the next Tests read |

The two I2e test questions are resolved by I2f. The plan's new I2f follow-ups remain open: whether to strip legacy Isolate on preset load, how white release should behave with fades, and masked-note refresh timing for raw/Identify changes. This refresh does not decide them. Deferred proposals remain documented, not silently removed or represented as shipped.

Baseline mockups now omit universe chips, P/T marks and TEST source tags. New review scenes show disarmed and in-flight/completed/stopped commands, cell virtual dimmers, stopped sequence editing and MIDI. They use fictional data and never send output. Original D0 screenshots and 132 checks are historical evidence; refreshed evidence is listed separately in verification.md.
