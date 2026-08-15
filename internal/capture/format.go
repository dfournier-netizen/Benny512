package capture

import (
	"fmt"
	"strings"
)

// FormatEntryText renders one Entry as a human-readable annotated block:
// timestamp, direction, kind/peer, decoded fields (when available), and a
// full hex dump. Used by both the continuous disk logger (one block per
// RDM/ToD entry as it happens) and the TXT export's per-exchange transcript
// (report task item 3: "human-readable annotated transcript... decoded
// fields, hex dump, so it can be pasted into a chat message").
func FormatEntryText(e Entry) string {
	var b strings.Builder
	peer := "-"
	if e.Peer.IsValid() {
		peer = e.Peer.String()
	}
	fmt.Fprintf(&b, "[%s] %s  %s  peer=%s  size=%d\n",
		e.Time.Format("2006-01-02 15:04:05.000"), dirLabel(e.Dir), e.Kind, peer, e.Size)

	switch {
	case e.RDM != nil:
		writeRDMDetailText(&b, e.RDM)
	case e.Tod != nil:
		writeTodDetailText(&b, e.Tod)
	case e.Key != "":
		fmt.Fprintf(&b, "  %s\n", e.Key)
	}

	if e.Hex != "" {
		fmt.Fprintf(&b, "  hex: %s\n", e.Hex)
	} else if e.HexTrunc {
		fmt.Fprintf(&b, "  hex: (omitted, %d bytes > capture threshold)\n", e.Size)
	}
	return b.String()
}

func dirLabel(d Direction) string {
	if d == DirOut {
		return "OUT"
	}
	return "IN "
}

func writeRDMDetailText(b *strings.Builder, d *RDMDetail) {
	if d.DecodeError != "" {
		fmt.Fprintf(b, "  DECODE ERROR: %s\n", d.DecodeError)
		return
	}
	dir := "REQUEST"
	if d.IsResponse {
		dir = "RESPONSE"
	}
	fmt.Fprintf(b, "  %s  %s  TN=%d  SubDevice=%d  checksum=%s\n",
		d.CommandClass, dir, d.TransactionNumber, d.SubDevice, validLabel(d.ChecksumValid))
	fmt.Fprintf(b, "  src=%s  dst=%s\n", d.SourceUID, d.DestUID)
	fmt.Fprintf(b, "  PID=0x%04X (%s)  PDL=%d\n", d.PID, d.PIDName, d.PDL)
	if d.IsResponse {
		fmt.Fprintf(b, "  responseType=%s", d.ResponseType)
		if d.MessageCount > 0 {
			fmt.Fprintf(b, "  messageCount=%d", d.MessageCount)
		}
		fmt.Fprintln(b)
		if d.ResponseType == "NACK_REASON" {
			fmt.Fprintf(b, "  NACK reason: 0x%04X (%s)\n", d.NackReasonCode, d.NackReasonName)
		}
		if d.ResponseType == "ACK_TIMER" {
			fmt.Fprintf(b, "  ACK_TIMER: raw=%d units -> %dms per E1.20 (10ms/unit), or %dms if raw is already ms\n",
				d.AckTimerRawUnits, d.AckTimerMsPerE120, d.AckTimerMsIfRawIsMs)
		}
	} else {
		fmt.Fprintf(b, "  portId=%d\n", d.PortID)
	}
	if d.Decoded != "" {
		fmt.Fprintf(b, "  decoded: %s\n", d.Decoded)
	}
	fmt.Fprintf(b, "  paramData: %s\n", nonEmpty(d.ParamDataHex, "(none)"))
}

func writeTodDetailText(b *strings.Builder, d *TodDetail) {
	fmt.Fprintf(b, "  ArtTod%s  command=0x%02X (%s)  net=%d\n", d.SubKind, d.Command, d.CommandName, d.Net)
	if len(d.Addresses) > 0 {
		fmt.Fprintf(b, "  addresses: %v\n", d.Addresses)
	}
	if d.SubKind == "Data" {
		fmt.Fprintf(b, "  rdmVersion=%d port=%d uidTotal=%d blockCount=%d\n", d.RdmVersion, d.Port, d.UidTotal, d.BlockCount)
		if len(d.UIDs) > 0 {
			fmt.Fprintf(b, "  uids: %s\n", strings.Join(d.UIDs, ", "))
		} else {
			fmt.Fprintln(b, "  uids: (none in this block)")
		}
	}
}

func validLabel(ok bool) string {
	if ok {
		return "valid"
	}
	return "INVALID"
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
