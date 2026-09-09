# Saved shows and rig tools

Use **Patch → New show** for a separate rig; use the saved-show selector to return to another. Edits save automatically. The Fixture Library is shared, but fixture commitments, groups, presets and baselines belong to each show. The same physical fixture can be committed in several shows.

## Before testing

Check the persistent show name, NIC and output status. **Stop all output** stops Rig Check and the DMX output engine; it is not an RDM Identify-off command or a stop for another console.

For multi-cell phase spacing, edit the patch entry's **Phase slots**: `0` selects automatic counting; `16` explicitly gives a Rayzor sixteen phase slots. Automatic counting prefers repeated GDTF geometry, then the live RDM sub-device count. It changes only phase spacing between root fixtures, not fixture counts or channel mappings. Previously imported profiles without geometry metadata need re-profiling or an override.

## Show tools

- **Needs attention:** cached collisions, commitment and configuration issues, with links to the relevant editor/inspector. Re-discover and re-read to refresh evidence.
- **Selection & groups:** choose entries and save a named group, or send the selection to Function check.
- **Test presets:** save the selected function tests and exact scope. Loading a preset stops output; press Start separately. A preset referencing a removed entry is rejected instead of silently testing a smaller rig.
- **Rig baselines:** record the patch and cached fixture settings, then Compare now or export a TXT report. A baseline may contain unresolved issues; its issue count is shown. This is not a physical rig acceptance test.
- **Offline rehearsal:** choose a fault scenario, confirm stopping live output, then Open rehearsal. The child UI is labelled REHEARSAL and uses fake lighting transport. Changes are temporary; building another replaces it. Rehearsal models normal controls and simple faults, not every device's firmware or RF behavior.
- **Show recovery:** restore the preceding save, export show JSON, or reset only this show. Reset clears that show's entries/groups/presets/baselines but keeps other shows and the Fixture Library.

Deleting a saved group, preset or baseline asks for confirmation. The preceding-save recovery copy includes the deleted item until another successful show save replaces that copy.

## Backup and recovery

The executable's directory is the data root. Keep `benny512-patch.json`, its `.bak`, the `benny512-patch.json.patches` directory and `.active` marker, library and Rig Walk JSON together when backing up. The `.bak` files provide ONE preceding save, not historical version control or a substitute for off-machine backup.

If the selected show is damaged, Benny512 will not silently switch to another rig. Open Show tools and restore its preceding save. If that copy is unavailable, close Benny512 and restore a known-good exported show JSON to the corresponding runtime file. Settings → Full Reset is different: it erases ALL shows and their recovery copies, retaining only the Fixture Library among those stores.

## Fixture library

Open **Fixture library** in the top strip. It is saved as `benny512-library.json` beside the executable, shared across shows and retained by Full Reset.

- **Import GDTF / library JSON:** preview, then Apply import. Modes merge into the matching fixture type. Original GDTFs imported here are retained and individually downloadable.
- **Save profiles from this patch:** remembers fixture types, modes and channel maps from the active show. It cannot recreate an original GDTF archive from a patch.
- **Mark verified:** confirm only after checking that specific mode's footprint/channel behavior on hardware. Importing a manufacturer file does not verify it. Changed mode data clears its old verification; identical reimports keep it.
- **Use in patch:** add an uncommitted entry, or explicitly apply the chosen profile to selected existing entries. Check address overlaps afterwards; use Reconcile to commit physical fixtures.
- **Export library JSON:** backs up/shares profiles, mode-verification records and retained original GDTFs together. Import that JSON on another installation. Source files are deduplicated by checksum. Imported verification is an operator claim, not a digital certificate.

GDTF import currently accepts files up to 8 MB; library import accepts exports up to 120 MB. Library saves are checked and protect existing damaged files, but there is no library `.bak` recovery yet. Keep exported backups. Older builds can discard newer verification/source-file fields if they rewrite the library; use this build for new-library round trips.

## Nodes and RDM mode names

Nodes lists physical devices on the left. Choose a parent, then use **Node settings** for shared names/IP settings or **Port settings** for a single port's universe, direction, RDM and merge settings. Both panes scroll independently. Names on a one-port-per-bind gateway remain port-specific.

Saving a port does not save hidden sibling drafts. Changes that would move other ports into another shared universe block are refused. Input-enable is unavailable on shared multi-port binds because ArtInput rewrites all enable bits and sibling readback is unavailable. IP changes retain Apply → Arm → Confirm. Always verify effects on the gateway.

Device Info and the Parameters mode selector fetch RDM mode names automatically. Current mode loads first, then remaining names sequentially and from cache where possible. A missing response is labelled “Mode name unavailable,” not guessed.

Benny512 is **Art-Net-only**. The sACN monitor and its mapping question have been removed.

## Devices discovery

**Discover this port** scans the selected output. **Discover all ports** scans
every currently discovered node's output ports, regardless of list filters.
The target list is frozen at the start and processed one port at a time.
**Stop after this port** lets the current request finish and skips the remainder.
Progress reports unique UIDs, failed ports and incomplete tables; a silent port
can still take the normal discovery timeout. Do not close the browser to stop
a scan: use its Stop control. A browser queue does not coordinate discovery
buttons in other browsers, so run one operator's discovery at a time.

Node filters group by physical IP. The port picker preserves reported names
and identifies separate Art-Net bindings explicitly; a binding-local index of
zero is not a physical faceplate port number. Routing retains the original IP,
binding and canonical universe. Node/RDM updates and connection recovery
refresh the list without reloading the page or replacing an inspector draft.

Device class is derived from reported RDM metadata, not the model name. The
Paladin Cubes in `Logs/RDM-LOG27.txt` report category `0x0509`, which ANSI E1.20
defines as a specialized LED dimmer. Benny512 consequently shows Dimmer/Power.
This is separate from the earlier hardware discovery fault; no model-specific
classification override is installed.

## Development verification and known limits

Use a disposable `--demo` installation for browser checks. Never launch the real installation just to test software changes. Go tests cover the storage, workspace, rehearsal and packet seams; physical hardware and race detection are separate gates.

The stock demo seeds its discovery tables but does not answer fresh ToD
requests. For end-to-end rediscovery checks, build a rehearsal from its demo
patch: the rehearsal responder answers ToD requests on fake transport.

The E1.37-2 network-configuration wire formats were corrected on 8 September 2026 against the primary text, including its Appendix B worked example (commit `55264ec`). They have not yet been confirmed against a real gateway, so verify on hardware before relying on these controls in the field. No authentication or untrusted-network hardening is added by this batch: operate on the intended trusted lighting LAN.
