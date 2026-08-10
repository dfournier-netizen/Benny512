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
	"net/http"
	"net/netip"
	"os"
	"os/signal"
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

	var srv *web.Server
	var closeTransport func()

	if *demo {
		logf("info", "starting in --demo mode: 2 fake nodes, 6 fake fixtures, synthetic ArtDmx traffic")
		srv = buildDemo(ctx)
	} else {
		var err error
		srv, closeTransport, err = buildReal(*iface, logf)
		if err != nil {
			logger.Fatalf("startup failed: %v", err)
		}
	}
	if closeTransport != nil {
		defer closeTransport()
	}

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
// chosen (or auto-selected) NIC.
func buildReal(ifaceName string, logf func(level, format string, args ...any)) (*web.Server, func(), error) {
	ifaces, err := transport.ListInterfaces()
	if err != nil {
		return nil, nil, fmt.Errorf("enumerate interfaces: %w", err)
	}
	chosen, err := selectInterface(ifaces, ifaceName)
	if err != nil {
		return nil, nil, err
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
		return nil, nil, fmt.Errorf("bind UDP: %w", err)
	}

	clock := session.RealClock{}
	nodes := session.NewArtNetSession(session.ArtNetConfig{Transport: udp, Clock: clock})
	rdmc := session.NewRDMController(session.RDMConfig{Transport: udp, Clock: clock})
	dmx := session.NewDMXOutputEngine(session.DMXConfig{Transport: udp, Clock: clock})
	reg := registry.New(nodes, rdmc)
	ring := capture.New(capture.DefaultCapacity)

	go reg.Run()
	go nodes.Run(context.Background())
	go rdmc.Run(context.Background())

	if err := nodes.Start(); err != nil {
		logf("warn", "initial ArtPoll failed: %v", err)
	}
	dmx.Start()

	srv := web.New(nodes, rdmc, dmx, reg, ring)
	return srv, func() { udp.Close() }, nil
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
