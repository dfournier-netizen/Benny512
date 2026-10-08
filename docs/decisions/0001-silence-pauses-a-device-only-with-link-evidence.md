# 0001 — Silence pauses a device only with link evidence

**Decided:** 2026-10-06 16:26:22 -0400, Dom (owner), branch `fix/rdm-unresponsive-device`.

**Decision.** A command that times out without a single response packet counts against its device only if some other command on the same node port was answered since that device's previous strike or answer. Three counted strikes pause the device (`CauseNoResponse`): its queued commands fail fast without transmitting, and the controller rechecks it with one GET DEVICE_INFO after a cool-down (15 s, doubling to 2 min while the port is proven alive; immediate when a ToD lists it again). Its pending commands move to the back of the port's queue after each silent command.

**Evidence.** RDM-LOG36: one dead ERA 800 (123 requests, 0 replies) starved two healthy fixtures on the same port, because commands are serialised per node port. RDM-LOG4: a 36 s node reboot silenced every device including healthy ones — the reason bare silence was never allowed to trip the breaker.

**Why this rule.** A dead fixture is silent while its neighbours answer; a node outage silences everyone at once, so between any two of a device's silences nothing answers and no second strike can count. The two cases are told apart by the link, not by timing guesses.

**Rejected.** (1) Counting every timeout — would blacklist the whole rig during a node reboot (LOG4). (2) "Another device answered in the last N seconds" — a reboot right after a healthy answer would still trip everyone. (3) Pausing without automatic recheck — a paused fixture that nothing asks would never come back.

**Revisit if.** A rig shows a single fixture on a port starving something (this rule cannot pause a lone fixture), or a gateway proves to answer for absent devices (which would hide silence entirely).

**Also decided the same day.** `ProfileDirect` response timeout 1500 → 500 ms (slowest first-attempt reply in 6,754 across LOG2–LOG36 was 96 ms; later replies were lost packets recovered by retransmit, so retries stay at 2). Responses not addressed to Benny512's controller UID are ignored (LOG36's second controller produced a TN/UID/class collision).
