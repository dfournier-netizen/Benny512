# E1.37-2 Network Configuration — Bench Test Procedure

**Goal:** find out whether Benny512's guess at the E1.37-2 IPv4/DNS packet
format is actually correct, using your EN4 and a LumenRadio unit. This is a
GO/NO-GO test, not a "does it look okay" test — follow it exactly and send
back the one file it asks for.

**Before you start:** everything you're about to touch is marked
"unverified against real hardware" in the app itself. That's not
boilerplate — it means a wrong byte in this code could genuinely tell a
gateway to change its own IP address to something wrong, which can take it
off the show network. Have the device's own front-panel or web UI open in
parallel the whole time so you can always see its real address, and don't
run this on a device mid-show.

## 1. Start logging BEFORE you touch anything

```
benny512 --logrdm bench-e1372.log
```

Leave this running for the entire test. Don't restart Benny512 partway
through — one continuous log file is what makes the round-trip below easy
to check by hand afterward.

## 2. Open the EN4's device page and look for the Network section

1. Open Benny512 in the browser, go to **Devices**.
2. Find the EN4's own gateway entry (not one of the fixtures behind it —
   the gateway's own RDM identity) and click into it.
3. Scroll to **Network configuration (E1.37-2)**.

**If the section is not there at all:** stop here. That means the EN4
never answered `LIST_INTERFACES` — either it doesn't implement E1.37-2, or
something upstream of the format is already wrong (wrong PID number, no
response at all). That's a useful, decisive result on its own — capture
5 minutes of the log and send it back, no need to continue.

**If the section is there:** you should see a caution banner, then one
box per network interface with whatever fields the EN4 actually answered
(current address, static address, DHCP, hardware address if it answered
that too). Take a screenshot of this box before you change anything.

Do the same for a LumenRadio wireless unit if you have one you can risk —
most fixtures/wireless units will NOT have this section at all, and that's
expected, not a bug.

## 3. Do one GET → SET → GET round trip

This is the part that actually proves or disproves the format.

1. In the interface box, note the **current** static IP/mask shown.
2. Type a **new** static IP in the same subnet (e.g. if current is
   `2.11.90.2 / 255.255.0.0`, try `2.11.90.99 / 255.255.0.0` — same
   subnet, so even if something goes sideways the device stays reachable
   from the same network segment).
3. Click **Apply**, then **Arm send…**, then confirm on the warning banner
   that names the device and the exact address. (Three clicks is
   deliberate — this is the one field in the app with the most ceremony
   in front of it.)
4. Watch the EN4's own front panel/web UI. Does its displayed address
   change to what you just sent?
5. Back in Benny512, refresh the device page (or just wait — it re-reads
   automatically after sending). Does the "Static address" field now show
   your new value?
6. Set it back to the original address the same way, and confirm the
   panel/web UI shows the original address again.

Do the same one time for the **DHCP** toggle if the EN4 offers one: flip
it on, confirm, check the front panel; flip it back off, confirm, check
again.

## 4. Stop logging and find the round trip in the log

Stop Benny512 (or just stop watching — the log file is already complete).
Open `bench-e1372.log` in a text editor and search for `IPV4_STATIC_ADDRESS`.
You're looking for pairs of lines like:

```
... SET_COMMAND        IPV4_STATIC_ADDRESS  paramDataHex=00000001020b5b6...  decoded="SET interface=1 ip=2.11.90.99 mask=255.255.0.0"
... SET_COMMAND_RESPONSE IPV4_STATIC_ADDRESS responseType=ACK
... GET_COMMAND        IPV4_STATIC_ADDRESS  paramDataHex=00000001
... GET_COMMAND_RESPONSE IPV4_STATIC_ADDRESS responseType=ACK decoded="interface=1 ip=2.11.90.99 mask=255.255.0.0"
```

Every line has BOTH the raw hex (`paramDataHex`) and Benny512's plain-
English guess at what it means (`decoded`) sitting right next to each
other — that's on purpose, so you can check the guess against the hex
by hand.

## 5. What each result means

- **The EN4's front panel/web UI actually changed to the address you
  sent, and changed back when you reverted it:** the format is right (at
  least for this PID, on this device). Good news — send the log anyway so
  we can confirm and use it as a template for the rest of the E1.37-2
  PIDs.

- **The SET got a NACK (`responseType=NACK_REASON` in the log), or the
  front panel never changed at all:** the format is probably wrong, or
  the EN4 doesn't implement this PID the way we guessed. Note the exact
  `nackReasonCode`/`nackReasonName` from the log line — that tells us
  whether the device rejected the PID entirely (`UNKNOWN_PID`) or
  understood the PID but rejected our byte layout (`FORMAT_ERROR` /
  `DATA_OUT_OF_RANGE` are the interesting ones).

- **The SET got ACKed, but the front panel shows something wrong or
  garbled (not the address you typed, not the old address either):**
  this is the most useful failure — it means the device accepted SOME
  bytes at SOME offset, which tells us the layout is close but not exact
  (e.g. IP and mask swapped, or an extra/missing length byte). Send the
  log; the hex should let us pin down exactly what shifted.

## 6. Send back

- `bench-e1372.log` (the whole thing — don't trim it, more context helps)
- The two screenshots from step 2
- A one-line note on what you observed on the EN4's own front panel/web UI
  at each step (this is the ground truth the log alone can't give us)
- Whether you also tried a LumenRadio unit, and what happened if so
