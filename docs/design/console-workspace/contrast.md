# Workspace contrast

Rebuild: `node docs/design/console-workspace/build-package.mjs`. Values resolve from production C9 roles. All permitted pairs, not just worst cases, are recorded below and in contrast.json. Essential boundaries ≥3:1, normal text ≥4.5:1, intensity words ≥7:1 on both track and fill. Shadows are decorative; visible opaque borders carry separation. Grid lines are decorative. Custom themes must validate these same aliases/pairs.

| Theme | Workspace role | Against | Ratio | Minimum | Result |
|---|---|---|---:|---:|---|
| dark | window-border | grid-bg | 6.66 | 3 | PASS |
| dark | window-border | bg-canvas | 6.92 | 3 | PASS |
| dark | window-border | bg-surface | 6.33 | 3 | PASS |
| dark | window-border | bg-surface-raised | 5.86 | 3 | PASS |
| dark | window-border | bg-selected | 4.75 | 3 | PASS |
| dark | dock-border | grid-bg | 6.66 | 3 | PASS |
| dark | dock-border | bg-surface | 6.33 | 3 | PASS |
| dark | drop-border | grid-bg | 10.40 | 3 | PASS |
| dark | drop-border | accent-fill | 8.57 | 3 | PASS |
| dark | intensity-edge | bg-inset | 11.09 | 3 | PASS |
| dark | intensity-edge | accent-fill | 8.57 | 3 | PASS |
| dark | intensity-text | bg-inset | 17.39 | 7 | PASS |
| dark | intensity-text | accent-fill | 13.44 | 7 | PASS |
| dark | tag-set | bg-surface | 9.88 | 4.5 | PASS |
| dark | tag-test | bg-surface | 9.85 | 4.5 | PASS |
| dark | tag-highlight | bg-surface | 10.51 | 4.5 | PASS |
| dark | window-bg | text-primary | 15.49 | 4.5 | PASS |
| dark | window-bg | text-secondary | 10.27 | 4.5 | PASS |
| dark | window-bg | text-muted | 7.59 | 4.5 | PASS |
| dark | drop-bg | text-primary | 13.44 | 4.5 | PASS |
| dark | drop-bg | text-secondary | 8.91 | 4.5 | PASS |
| dark | drop-bg | text-muted | 6.58 | 4.5 | PASS |
| dark | intensity-track | text-primary | 17.39 | 4.5 | PASS |
| dark | intensity-track | text-secondary | 11.53 | 4.5 | PASS |
| dark | intensity-track | text-muted | 8.52 | 4.5 | PASS |
| dark | intensity-fill | text-primary | 13.44 | 4.5 | PASS |
| dark | intensity-fill | text-secondary | 8.91 | 4.5 | PASS |
| dark | intensity-fill | text-muted | 6.58 | 4.5 | PASS |
| dark | tag-bg | text-primary | 15.49 | 4.5 | PASS |
| dark | tag-bg | text-secondary | 10.27 | 4.5 | PASS |
| dark | tag-bg | text-muted | 7.59 | 4.5 | PASS |
| light | window-border | grid-bg | 5.50 | 3 | PASS |
| light | window-border | bg-canvas | 5.07 | 3 | PASS |
| light | window-border | bg-surface | 5.94 | 3 | PASS |
| light | window-border | bg-surface-raised | 5.50 | 3 | PASS |
| light | window-border | bg-selected | 4.71 | 3 | PASS |
| light | dock-border | grid-bg | 5.50 | 3 | PASS |
| light | dock-border | bg-surface | 5.94 | 3 | PASS |
| light | drop-border | grid-bg | 7.01 | 3 | PASS |
| light | drop-border | accent-fill | 6.00 | 3 | PASS |
| light | intensity-edge | bg-inset | 6.01 | 3 | PASS |
| light | intensity-edge | accent-fill | 6.00 | 3 | PASS |
| light | intensity-text | bg-inset | 13.88 | 7 | PASS |
| light | intensity-text | accent-fill | 13.86 | 7 | PASS |
| light | tag-set | bg-surface | 7.57 | 4.5 | PASS |
| light | tag-test | bg-surface | 8.33 | 4.5 | PASS |
| light | tag-highlight | bg-surface | 8.62 | 4.5 | PASS |
| light | window-bg | text-primary | 17.48 | 4.5 | PASS |
| light | window-bg | text-secondary | 10.81 | 4.5 | PASS |
| light | window-bg | text-muted | 7.37 | 4.5 | PASS |
| light | drop-bg | text-primary | 13.86 | 4.5 | PASS |
| light | drop-bg | text-secondary | 8.57 | 4.5 | PASS |
| light | drop-bg | text-muted | 5.84 | 4.5 | PASS |
| light | intensity-track | text-primary | 13.88 | 4.5 | PASS |
| light | intensity-track | text-secondary | 8.58 | 4.5 | PASS |
| light | intensity-track | text-muted | 5.85 | 4.5 | PASS |
| light | intensity-fill | text-primary | 13.86 | 4.5 | PASS |
| light | intensity-fill | text-secondary | 8.57 | 4.5 | PASS |
| light | intensity-fill | text-muted | 5.84 | 4.5 | PASS |
| light | tag-bg | text-primary | 17.48 | 4.5 | PASS |
| light | tag-bg | text-secondary | 10.81 | 4.5 | PASS |
| light | tag-bg | text-muted | 7.37 | 4.5 | PASS |
