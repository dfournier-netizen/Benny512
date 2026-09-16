// Command benny512 is the server half of Benny512: it binds Art-Net,
// drives the session engines, and serves the embedded browser UI over
// HTTP+WebSocket. See rdm-app-architecture_2026-08-09_2236.md §3 for the
// package layout this wires together.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"benny512/internal/capture"
	"benny512/internal/registry"
	"benny512/internal/session"
	"benny512/internal/transport"
	"benny512/internal/web"
)

func main() {
	port := flag.Int("port", web.DefaultPort, "HTTP port to serve the UI and API on")
	iface := flag.String("iface", "", "network interface name to bind for Art-Net (default: first non-loopback IPv4 interface)")
	demo := flag.Bool("demo", false, "run against fake pre-scripted nodes/fixtures instead of real hardware")
	logLevel := flag.String("loglevel", "info", "log verbosity: debug|info|warn|error")
	logRDM := flag.String("logrdm", "", "optional: continuously append every RDM/ToD exchange to this file as it happens (rotates by size)")
	logNodes := flag.Bool("lognodes", false, "with --logrdm: also log Art-Net node-configuration traffic to the same file — the complete ArtAddress/ArtInput/ArtIpProg/ArtIpProgReply history (every instance, since these are rare and user-initiated), plus a bounded sample of the periodic ArtPoll/ArtPollReply background chatter (per-node, since that repeats for the life of the session). Has no effect without --logrdm.")
	legacyRdmStartCode := flag.Bool("legacy-rdm-startcode", false, "escape hatch: include the 0xCC RDM start code in outbound ArtRdm payloads (pre-fix, spec-incorrect framing). Default false sends the spec-correct payload starting at the RDM sub-start code (0x01). Inbound decode accepts both forms either way.")
	flag.Parse()

	logger := log.New(os.Stdout, "", log.LstdFlags)
	logf := func(level, format string, args ...any) {
		if !shouldLog(*logLevel, level) {
			return
		}
		logger.Printf("[%s] "+format, append([]any{level}, args...)...)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		logf("info", "shutdown signal received, stopping…")
		cancel()
	}()

	if *legacyRdmStartCode {
		logf("info", "ArtRdm outbound framing: LEGACY (0xCC start code included in RdmPacket — spec-incorrect, --legacy-rdm-startcode set)")
	} else {
		logf("info", "ArtRdm outbound framing: spec-correct (RdmPacket begins at the 0x01 sub-start code, no leading 0xCC)")
	}

	// buildDemo/buildReal only construct — engines, capture tap, the
	// server — and hand back a start func that is not called until this
	// function has finished arming everything traffic could possibly touch
	// (--logrdm's disk logger above all: see buildReal's doc comment for
	// why calling start before that is armed would permanently lose
	// exactly the packets --lognodes exists to capture, ArtPollReply
	// included). Nothing above start() may generate a single packet.
	var srv *web.Server
	var start func()
	var closeTransport func()

	if *demo {
		logf("info", "starting in --demo mode: 2 fake nodes, 6 fake fixtures, synthetic ArtDmx traffic")
		srv, start = buildDemo(ctx, *legacyRdmStartCode, *logNodes)
	} else {
		var err error
		srv, start, closeTransport, err = buildReal(*iface, *legacyRdmStartCode, *logNodes, logf)
		if err != nil {
			logger.Fatalf("startup failed: %v", err)
		}
	}
	if closeTransport != nil {
		defer closeTransport()
	}
	defer srv.Close()
	srv.OnRehearse = makeRehearsalLauncher(ctx)

	// Wire the full-reset flow's "Reset and exit" behavior (task ask; see
	// internal/web's reset.go) to the exact same context-cancel func the
	// SIGINT/SIGTERM handler above already uses, so a reset-triggered
	// shutdown goes through the identical clean-shutdown path (HTTP server
	// Shutdown, deferred srv.Close/closeTransport) rather than a second,
	// divergent one.
	srv.OnShutdownRequest = func(reason string) {
		logf("info", "shutdown requested (%s)", reason)
		cancel()
	}

	// Rig Walk mode persists its session as a JSON file next to the exe
	// (task ask: "a simple in-memory session plus JSON file next to the exe
	// is fine, matching existing persistence conventions") so a dropped
	// phone connection, accidental refresh, or even a server restart mid-walk
	// doesn't lose progress. Demo mode has no on-disk session to resume (its
	// walk always starts empty either way) but --demo's patch is different:
	// buildDemo pre-loads a sample patch directly into srv.PatchStore so the
	// whole reconcile/rig-check flow is exercisable with no hardware (task
	// ask, item 5) — switching PatchStore to a fresh on-disk-backed Store
	// here would silently discard that in-memory sample (there's normally no
	// benny512-patch.json next to a freshly-unzipped demo build, so the new
	// Store would just come up empty). Only real mode gets file persistence;
	// --demo keeps its preloaded in-memory patch for the life of the process.
	srv.SetWalkStorePath(walkSessionPath())
	if !*demo {
		srv.SetPatchStorePath(patchStorePath())
	}
	// The Fixture Library persists in EVERY mode, --demo included, and is
	// wired unconditionally — unlike the patch above, which --demo skips to
	// protect buildDemo's preloaded in-memory sample. Nothing preloads the
	// library, so there is nothing for an on-disk store to discard here; and
	// the library is precisely the thing that is supposed to outlive a
	// process, a show and a reset (see internal/library's package doc
	// comment), so a mode in which it silently evaporated on exit would be
	// the same bug this call fixes.
	srv.SetLibraryStorePath(libraryStorePath())
	// The sACN configuration persists in EVERY mode, --demo included, for
	// the same reason the library does: it is this INSTALLATION's identity
	// and network configuration (a CID generated once and reused for the
	// life of the install, the sACN start universe, priority and optional
	// unicast destination), not one show's data. A failed save is logged and
	// survived — an unwritable directory must not stop the server coming up,
	// and SetSACNStorePath always leaves a usable in-memory configuration.
	if err := srv.SetSACNStorePath(sacnStorePath()); err != nil {
		logf("warn", "sACN settings: %v", err)
	}

	if *logRDM != "" {
		if err := srv.SetLogRDMPath(*logRDM); err != nil {
			logger.Fatalf("--logrdm: %v", err)
		}
		logf("info", "logging RDM/ToD traffic to %s", *logRDM)
		if *logNodes {
			logf("info", "also logging Art-Net node-configuration traffic (--lognodes): full ArtAddress/ArtInput/ArtIpProg/ArtIpProgReply history, plus a bounded sample of ArtPoll/ArtPollReply")
		}
	} else if *logNodes {
		logf("warn", "--lognodes has no effect without --logrdm (there is no disk log to write to)")
	}

	// Everything that can generate a packet the capture tap would see
	// starts here — after OnShutdownRequest, the walk/patch store paths,
	// and --logrdm are all wired above. This is the fix for the disk log
	// being able to miss its own seed traffic (both demo nodes' initial
	// ArtPollReply, the demo device-cache RDM warmup, and in real mode the
	// first ArtPoll) — see buildDemo/buildReal's doc comments.
	start()

	go srv.Run(ctx)

	addr := fmt.Sprintf(":%d", *port)
	httpSrv := &http.Server{Addr: addr, Handler: srv.Handler()}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	localIP := "127.0.0.1"
	if !*demo {
		if ip, ok := firstDisplayIP(*iface); ok {
			localIP = ip
		}
	}
	logf("info", "open http://%s:%d", localIP, *port)

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("http server: %v", err)
	}
	logf("info", "stopped cleanly")
}

// buildReal wires every engine against the real UDP transport bound to the
// chosen (or auto-selected) NIC, and returns a start func that begins
// actually driving traffic (demux dispatch, the engines' Run loops, and the
// initial ArtPoll/DMX output) — see this function's call site in main() and
// buildDemo's doc comment for why construction and traffic generation are
// split the same way in both builders: --logrdm's disk logger is armed by
// the caller *after* getting srv back from here, so nothing here may
// generate a packet the tap could observe before start is called, or that
// packet's entry in the log is lost for good (LogRDMEntry silently no-ops
// until a logger is configured — it doesn't buffer).
func buildReal(ifaceName string, legacyRdmStartCode bool, logNodes bool, logf func(level, format string, args ...any)) (srv *web.Server, start func(), closeTransport func(), err error) {
	ifaces, err := transport.ListInterfaces()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("enumerate interfaces: %w", err)
	}
	chosen, err := selectInterface(ifaces, ifaceName)
	if err != nil {
		return nil, nil, nil, err
	}
	logf("info", "using interface %q (%v)", chosen.Name, chosen.IPv4)

	bindAddr, _ := netip.ParseAddr(chosen.IPv4[0])
	addr, prefix, ok := transport.SubnetAddrPrefix(chosen.Name)
	bcast := netip.MustParseAddr("255.255.255.255")
	if ok {
		if b, ok2 := transport.BroadcastAddrFor(addr, prefix); ok2 {
			bcast = b
		}
	}

	udp, err := transport.Listen(transport.Config{BindAddr: bindAddr, BroadcastAddr: bcast})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("bind UDP: %w", err)
	}

	// Demux fixes a real bug that only shows up with more than one real-mode
	// consumer of udp.Inbound(): a plain Go channel delivers each datagram
	// to exactly one reader, so handing `udp` directly to three engines
	// (as this function used to) silently split inbound traffic between
	// ArtNetSession and RDMController at random. Demux fans every inbound
	// datagram out to all three, and is also the single choke point that
	// feeds both capture rings with real traffic in both directions — see
	// internal/session/demux.go's doc comment for the full story.
	demux := session.NewDemux(udp)

	clock := session.RealClock{}
	nodes := session.NewArtNetSession(session.ArtNetConfig{Transport: demux.Subscriber(), Clock: clock})
	rdmc := session.NewRDMController(session.RDMConfig{Transport: demux.Subscriber(), Clock: clock, LegacyRdmStartCode: legacyRdmStartCode})
	dmx := session.NewDMXOutputEngine(session.DMXConfig{Transport: demux.Subscriber(), Clock: clock})
	reg := registry.New(nodes, rdmc)
	ring := capture.New(capture.DefaultCapacity)
	rdmRing := capture.New(capture.DefaultRDMCapacity)
	// unknownOpcodeThrottle bounds the separate, capped route into the RDM
	// diagnostic stream for inbound datagrams with a valid envelope but an
	// opcode we don't decode (see capture.UnknownOpcodeThrottle) — a
	// legitimate high-rate opcode like ArtSync must not flood the RDM-only
	// ring the way an occasional genuine decode failure safely can't.
	unknownOpcodeThrottle := capture.NewUnknownOpcodeThrottle(0)
	// periodicNodeThrottle is unknownOpcodeThrottle's counterpart for
	// ArtPoll/ArtPollReply once --lognodes is set (see
	// capture.PeriodicNodeThrottle's doc comment) — built regardless of
	// logNodes so it's always available, but only ever consulted when
	// logNodes is true.
	periodicNodeThrottle := capture.NewPeriodicNodeThrottle(0)

	srv = web.New(nodes, rdmc, dmx, reg, ring, rdmRing)
	srv.NIC = fmt.Sprintf("%s (%v)", chosen.Name, chosen.IPv4)
	// Retain the NIC we actually chose, not just its display string. The
	// Art-Net transport above is bound here and then the choice was thrown
	// away; an sACN sender opens its own socket later, on demand, and has to
	// be told which interface to leave by or the routing table decides for
	// it (see web.Server.OutputInterface). Neither lookup is fatal: a nil
	// interface or IP leaves the choice to the OS, which is exactly what
	// --demo and every test already get.
	if ifi, err := net.InterfaceByName(chosen.Name); err == nil {
		srv.OutputInterface = ifi
	} else {
		logf("warn", "sACN: could not re-resolve interface %q (%v); leaving the egress NIC to the routing table", chosen.Name, err)
	}
	srv.OutputBindIP = net.ParseIP(chosen.IPv4[0])

	tap := func(dir capture.Direction, peer netip.AddrPort, data []byte) {
		e := capture.DecodeEntry(dir, peer, data)
		// Add stamps Time/Seq on its own returned copy (the zero-value Time
		// from DecodeEntry only gets set inside Add) — use that stamped
		// copy for the RDM ring and disk logger, not the pre-Add value,
		// or every disk-logged/second-ring entry gets a zero timestamp.
		e = ring.Add(e)
		switch {
		case capture.IsRDMLoggable(e):
			e = rdmRing.Add(e)
			srv.LogRDMEntry(e)
		case logNodes && capture.IsNodeConfigKind(e.Kind):
			// Rare, user-initiated node-configuration traffic (ArtAddress/
			// ArtInput/ArtIpProg/ArtIpProgReply): every instance logged in
			// full, unconditionally — see IsNodeConfigKind's doc comment.
			e = rdmRing.Add(e)
			srv.LogRDMEntry(e)
		case logNodes && capture.IsPeriodicNodeKind(e.Kind):
			// Periodic background chatter (ArtPoll/ArtPollReply): bounded
			// per-(peer,kind) sample, not every instance — see
			// IsPeriodicNodeKind's doc comment.
			if logEntry, ok := periodicNodeThrottle.Consider(e); ok {
				logEntry = rdmRing.Add(logEntry)
				srv.LogRDMEntry(logEntry)
			}
		default:
			if logEntry, ok := unknownOpcodeThrottle.Consider(e); ok {
				logEntry = rdmRing.Add(logEntry)
				srv.LogRDMEntry(logEntry)
			}
		}
	}
	// Wiring the hooks is safe here — a func value assignment, not a send —
	// but nothing may actually run yet: see this function's doc comment.
	demux.OnSend = func(data []byte, dst netip.AddrPort) { tap(capture.DirOut, dst, data) }
	demux.OnReceive = func(data []byte, from netip.AddrPort) { tap(capture.DirIn, from, data) }

	// start is deferred to the caller — see this function's doc comment.
	// demux.Start (begins dispatching whatever the OS socket already has
	// buffered), the engines' Run loops, nodes.Start's initial ArtPoll, and
	// dmx.Start's periodic output are the only things in this function that
	// can put a packet in front of the tap, so they all live here.
	start = func() {
		demux.Start()
		go reg.Run()
		go nodes.Run(context.Background())
		go rdmc.Run(context.Background())
		if err := nodes.Start(); err != nil {
			logf("warn", "initial ArtPoll failed: %v", err)
		}
		dmx.Start()
	}

	return srv, start, func() { udp.Close() }, nil
}

func selectInterface(ifaces []transport.Interface, want string) (transport.Interface, error) {
	if want != "" {
		for _, i := range ifaces {
			if i.Name == want {
				return i, nil
			}
		}
		return transport.Interface{}, fmt.Errorf("interface %q not found", want)
	}
	for _, i := range ifaces {
		if !i.Loopback && i.Up && len(i.IPv4) > 0 {
			return i, nil
		}
	}
	if len(ifaces) > 0 {
		return ifaces[0], nil
	}
	return transport.Interface{}, fmt.Errorf("no usable network interface found")
}

func firstDisplayIP(ifaceName string) (string, bool) {
	ifaces, err := transport.ListInterfaces()
	if err != nil {
		return "", false
	}
	chosen, err := selectInterface(ifaces, ifaceName)
	if err != nil || len(chosen.IPv4) == 0 {
		return "", false
	}
	return chosen.IPv4[0], true
}

// walkSessionPath resolves Rig Walk mode's persisted-session file to a path
// next to the running exe (falls back to the current working directory if
// os.Executable fails, e.g. some sandboxed test environments) — mirrors the
// "delivery model is the point: a single self-contained exe" convention the
// rest of the app follows for anything written to disk.
func walkSessionPath() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "benny512-rigwalk.json")
	}
	return "benny512-rigwalk.json"
}

// patchStorePath resolves the patch model's persisted file to a path next
// to the running exe — mirrors walkSessionPath exactly (task ask, item 1:
// "JSON file beside the exe (benny512-patch.json), same pattern as the
// existing rig-walk store").
func patchStorePath() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "benny512-patch.json")
	}
	return "benny512-patch.json"
}

// libraryStorePath resolves the Fixture Library's persisted file to a path
// next to the running exe — mirrors patchStorePath exactly. Deliberately a
// SEPARATE file from the patch: one library sits underneath every rig the
// owner ever patches (see internal/library's package doc comment), so it
// must not share a lifetime — or a delete — with any one show's patch file.
func libraryStorePath() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "benny512-library.json")
	}
	return "benny512-library.json"
}

// sacnStorePath resolves the persisted sACN configuration to a path next to
// the running exe — mirrors libraryStorePath exactly. A separate file from
// the patch and the library for the same reason they are separate from each
// other: it outlives every show, and the CID inside it must survive a full
// reset of this rig's state, so it must not share a lifetime — or a delete —
// with any one show's file.
func sacnStorePath() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "benny512-sacn.json")
	}
	return "benny512-sacn.json"
}

func shouldLog(configured, level string) bool {
	order := map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}
	c, ok := order[configured]
	if !ok {
		c = 1
	}
	l, ok := order[level]
	if !ok {
		l = 1
	}
	return l >= c
}
