package gdtfshare

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to GDTF Share. It holds its own cookie jar, so one Client is
// one GDTF Share session; the page does not name the session cookie, so the
// jar keeps whatever login.php sets.
//
// Every connection goes through guardedDial: see that function for how the
// lighting network is kept out of it.
type Client struct {
	base *url.URL
	http *http.Client
	// OnConnect, when set, is told the local address of every connection
	// guardedDial lets through (the TCP connection's own LocalAddr). Set it
	// before the first request.
	OnConnect func(local netip.Addr)
}

// NewClient builds a client for baseURL (DefaultBaseURL in production).
// lighting reports the subnets of the adapters selected for Art-Net and sACN
// output; nil means none are configured. timeout bounds each request.
func NewClient(baseURL string, lighting func() []netip.Prefix, timeout time.Duration) (*Client, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("GDTF Share address %q: %w", baseURL, err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	c := &Client{base: base}
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: guardedDial(lighting, func(a netip.Addr) {
			if c.OnConnect != nil {
				c.OnConnect(a)
			}
		}),
		TLSHandshakeTimeout: 30 * time.Second,
		MaxIdleConns:        2,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	c.http = &http.Client{Jar: jar, Transport: tr, Timeout: timeout}
	return c, nil
}

// guardedDial refuses any connection that would leave by the lighting
// network (owner decision, round 2, 3).
//
// Two checks, both stdlib:
//
//  1. Before connecting, for each resolved address, a UDP "connect" asks the
//     operating system which local address its routing table would use. A
//     UDP connect sends no packet, so when the route is the lighting adapter
//     nothing at all goes onto the lighting network.
//  2. After the TCP connection is made, conn.LocalAddr() is checked again —
//     the authoritative answer — and the connection is closed if it landed
//     on a lighting subnet anyway.
//
// Not covered: the operating system's own DNS lookup, which this process does
// not route.
//
// connected is told the local address of each connection it lets through.
func guardedDial(lighting func() []netip.Prefix, connected func(netip.Addr)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	onLighting := func(a netip.Addr) bool {
		if lighting == nil {
			return false
		}
		a = a.Unmap()
		for _, p := range lighting() {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		refused := false
		for _, ip := range ips {
			target := net.JoinHostPort(ip.Unmap().String(), port)
			if local, ok := routeLocalAddr(target); ok && onLighting(local) {
				refused = true
				continue
			}
			conn, err := d.DialContext(ctx, network, target)
			if err != nil {
				lastErr = err
				continue
			}
			if ta, ok := conn.LocalAddr().(*net.TCPAddr); ok {
				la, ok := netip.AddrFromSlice(ta.IP)
				if ok && onLighting(la) {
					conn.Close()
					refused = true
					continue
				}
				if ok && connected != nil {
					connected(la.Unmap())
				}
			}
			return conn, nil
		}
		if refused && lastErr == nil {
			return nil, ErrLightingRoute
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("no address for %s", host)
		}
		return nil, lastErr
	}
}

// routeLocalAddr reports the local address the routing table picks for
// target, without sending anything (a connected UDP socket).
func routeLocalAddr(target string) (netip.Addr, bool) {
	c, err := net.Dial("udp", target)
	if err != nil {
		return netip.Addr{}, false
	}
	defer c.Close()
	ua, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok {
		return netip.Addr{}, false
	}
	a, ok := netip.AddrFromSlice(ua.IP)
	return a, ok
}

// classify turns a transport error into this package's vocabulary.
func classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrLightingRoute) {
		return ErrLightingRoute
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return &OfflineError{Err: err}
}

func (c *Client) endpoint(name string, q url.Values) string {
	u := c.base.ResolveReference(&url.URL{Path: name})
	if q != nil {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// apiReply is the JSON envelope every documented answer shares.
type apiReply struct {
	Result *bool           `json:"result"`
	Error  string          `json:"error"`
	Notice string          `json:"notice"`
	List   json.RawMessage `json:"list"`
}

func readReply(op string, resp *http.Response, limit int64) ([]byte, apiReply, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, apiReply{}, classify(err)
	}
	var rep apiReply
	_ = json.Unmarshal(body, &rep)
	return body, rep, nil
}

// Login posts the documented JSON body. 200 with result true is success;
// 401 is a refused login. The password goes only into the request body —
// never into a URL, an error or a log line.
func (c *Client) Login(ctx context.Context, user, password string) error {
	payload, err := json.Marshal(struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}{user, password})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("login.php", nil), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return classify(err)
	}
	defer resp.Body.Close()
	_, rep, err := readReply("login", resp, 1<<20)
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrBadLogin
	case resp.StatusCode != http.StatusOK:
		return &StatusError{Op: "login", Code: resp.StatusCode, Message: rep.Error}
	case rep.Result == nil || !*rep.Result:
		// 200 without result:true is not documented; treat it as refused
		// rather than as a session we do not have.
		return &StatusError{Op: "login", Code: resp.StatusCode, Message: "no result:true in the answer " + rep.Error}
	}
	return nil
}

// maxListBytes bounds the getList answer. The whole of GDTF Share is some
// thousands of entries; 256 MB is far beyond that and still bounded.
const maxListBytes = 256 << 20

// List fetches getList.php. It returns the entries it could read and how
// many it had to skip (see parseList).
func (c *Client) List(ctx context.Context) ([]Entry, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("getList.php", nil), nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, classify(err)
	}
	defer resp.Body.Close()
	body, rep, err := readReply("list", resp, maxListBytes)
	if err != nil {
		return nil, 0, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, 0, ErrUnauthorized
	default:
		return nil, 0, &StatusError{Op: "list", Code: resp.StatusCode, Message: rep.Error}
	}
	return parseList(body)
}

// Download streams downloadFile.php?rid= into w, at most max bytes. The
// answer is accepted as a file only if it is HTTP 200 and begins with a ZIP
// local-file signature (a .gdtf is a ZIP); documented errors come back as
// JSON, so a JSON body is never mistaken for a file. Nothing is written to w
// unless the signature matched.
func (c *Client) Download(ctx context.Context, rid int64, w io.Writer, max int64) (int64, error) {
	q := url.Values{"rid": {strconv.FormatInt(rid, 10)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("downloadFile.php", q), nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, classify(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, rep, err := readReply("download", resp, 1<<20)
		if err != nil {
			return 0, err
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return 0, ErrUnauthorized
		}
		return 0, &StatusError{Op: fmt.Sprintf("download of rid %d", rid), Code: resp.StatusCode, Message: rep.Error}
	}
	br := bufio.NewReader(resp.Body)
	head, err := br.Peek(4)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, fmt.Errorf("GDTF Share sent rid %d as an empty or truncated file", rid)
		}
		return 0, classify(err)
	}
	if !bytes.Equal(head, []byte("PK\x03\x04")) {
		rest, _ := io.ReadAll(io.LimitReader(br, 1<<16))
		var rep apiReply
		if json.Unmarshal(rest, &rep) == nil && rep.Error != "" {
			if strings.Contains(strings.ToLower(rep.Error), "unauthorized") {
				return 0, ErrUnauthorized
			}
			return 0, fmt.Errorf("GDTF Share did not send rid %d: %s", rid, rep.Error)
		}
		return 0, fmt.Errorf("GDTF Share sent rid %d but it is not a GDTF (ZIP) file", rid)
	}
	n, err := io.Copy(w, io.LimitReader(br, max+1))
	if err != nil {
		return n, classify(err)
	}
	if n > max {
		return n, fmt.Errorf("rid %d is larger than the %d MB limit", rid, max>>20)
	}
	if resp.ContentLength > 0 && n != resp.ContentLength {
		return n, fmt.Errorf("rid %d arrived truncated (%d of %d bytes)", rid, n, resp.ContentLength)
	}
	return n, nil
}

// parseList reads getList's answer.
//
// Documented vs observed: the page's field table types creationDate and
// lastModified as Long and rating as Float, while its own example quotes all
// three as strings. Every numeric field is therefore accepted as a JSON
// number or as a quoted number. A value that is neither (empty, null) is
// "not given": dates and filesize become 0, rating and dmxfootprint null.
//
// An entry whose rid cannot be read is skipped and counted, so one malformed
// entry never sinks the whole list. A top-level answer without result:true
// and a list array is an error.
func parseList(body []byte) ([]Entry, int, error) {
	var env struct {
		Result *bool             `json:"result"`
		Error  string            `json:"error"`
		List   []json.RawMessage `json:"list"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, 0, fmt.Errorf("GDTF Share's list is not readable JSON: %w", err)
	}
	if env.Result == nil || !*env.Result || env.List == nil {
		msg := env.Error
		if msg == "" {
			msg = "no result:true with a list"
		}
		return nil, 0, fmt.Errorf("GDTF Share's list answer was refused: %s", msg)
	}
	out := make([]Entry, 0, len(env.List))
	skipped := 0
	for _, raw := range env.List {
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			skipped++
			continue
		}
		rid, ok := numInt(m["rid"])
		if !ok || rid <= 0 {
			skipped++
			continue
		}
		e := Entry{
			RID:          rid,
			Fixture:      str(m["fixture"]),
			Manufacturer: str(m["manufacturer"]),
			Revision:     str(m["revision"]),
			Uploader:     str(m["uploader"]),
			Version:      str(m["version"]),
			Creator:      str(m["creator"]),
			UUID:         str(m["uuid"]),
			Modes:        make([]Mode, 0),
		}
		e.CreationDate, _ = numInt(m["creationDate"])
		e.LastModified, _ = numInt(m["lastModified"])
		e.FileSize, _ = numInt(m["filesize"])
		if r, ok := numFloat(m["rating"]); ok {
			e.Rating = &r
		}
		var modes []map[string]json.RawMessage
		if json.Unmarshal(m["modes"], &modes) == nil {
			for _, md := range modes {
				mode := Mode{Name: str(md["name"])}
				if fp, ok := numInt(md["dmxfootprint"]); ok {
					v := int(fp)
					mode.DMXFootprint = &v
				}
				e.Modes = append(e.Modes, mode)
			}
		}
		e.UploaderLabel = uploaderLabel(e.Uploader)
		out = append(out, e)
	}
	return out, skipped, nil
}

func uploaderLabel(raw string) string {
	switch strings.TrimSpace(raw) {
	case "Manuf.":
		return LabelManufacturer
	case "User":
		return LabelUser
	}
	return LabelUnknown
}

func str(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

func numText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String(), true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s), true
	}
	return "", false
}

func numInt(raw json.RawMessage) (int64, bool) {
	t, ok := numText(raw)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseInt(t, 10, 64)
	return v, err == nil
}

func numFloat(raw json.RawMessage) (float64, bool) {
	t, ok := numText(raw)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseFloat(t, 64)
	return v, err == nil
}
