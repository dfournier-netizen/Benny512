// Package gdtfshare keeps a local library of GDTF files in step with
// GDTF Share (gdtf-share.com), through its public API:
//
//	POST https://gdtf-share.com/apis/public/login.php        {"user","password"} -> session cookie
//	GET  https://gdtf-share.com/apis/public/getList.php       -> {"result":true,"list":[...]}
//	GET  https://gdtf-share.com/apis/public/downloadFile.php?rid=N -> the .gdtf bytes
//
// Source for every request and response shape in this package: the GDTF
// Share API page, https://gdtf-share.com/help/developers/gdtf_share_dev/
// gdtf_share_api_1/index.html (read 2026-11-17). Where that page is vague
// the code says so beside the decision rather than inventing a field; see
// parseList in particular. Nothing here has yet been exercised against the
// real GDTF Share — the sandbox that built it cannot reach it. Chunk G4 (the
// owner's bench sync) is the first real contact.
//
// The library is a raw file cache plus an index, deliberately separate from
// the Fixture Library (internal/library): picking a fixture from it feeds the
// existing browser import path (gdtfparse.js -> POST /api/library/import).
//
// On disk, under one root (in production %APPDATA%\Benny512\GDTF\):
//
//	files/<rid>.gdtf   one file per downloaded revision, written to a .part
//	                   name and renamed, so a killed sync never leaves a half
//	                   file under a real name
//	index.json         every downloaded file: what getList said about it,
//	                   plus the local SHA-256, size and download time
//	sync.json          followed manufacturers, last list fetch, last result
//	                   per manufacturer, pending/failed downloads
//	list.json          the last getList answer, so the whole of GDTF Share
//	                   can be searched offline for a single pull
package gdtfshare

import (
	"errors"
	"fmt"
	"time"
)

// DefaultBaseURL is the documented API root. The three endpoints are
// resolved relative to it.
const DefaultBaseURL = "https://gdtf-share.com/apis/public/"

// DefaultManufacturers is the owner's starting list (2026-11-17). The user
// can add and remove names; matching against GDTF Share is case-insensitive
// and exact — no aliases are guessed, and a name that matches nothing is
// reported, never dropped.
var DefaultManufacturers = []string{
	"Ayrton", "Martin", "Elation", "Robe", "Chauvet", "GLP", "Volux", "ADJ",
	"ACME", "SGM", "Astera", "ChromaQ", "ETC", "High End Systems",
}

// Uploader labels. The API's uploader field is "Manuf." or "User"; the API
// terms require every listed file to carry that distinction, so it is turned
// into words here once. A value the page does not document is labelled
// Unknown rather than guessed into one of the two.
const (
	LabelManufacturer = "Manufacturer"
	LabelUser         = "User"
	LabelUnknown      = "Unknown"
)

var (
	// ErrBadLogin: login.php answered 401 (the page's example: "No valid
	// user or password provided.").
	ErrBadLogin = errors.New("GDTF Share refused the user name or password")
	// ErrUnauthorized: getList/downloadFile answered 401 ("Unauthorized.").
	// The session cookie lapses after two hours of inactivity.
	ErrUnauthorized = errors.New("GDTF Share says this session is not logged in")
	// ErrLightingRoute: the connection would leave by the lighting network
	// adapter. Owner decision (round 2, 3): never sync over the lighting
	// network.
	ErrLightingRoute = errors.New("the only route to the internet is the lighting network adapter")
	// ErrBusy: a sync, a single pull or a login test is already talking to
	// GDTF Share. Only one runs at a time.
	ErrBusy = errors.New("a GDTF Share sync is already running")
	// ErrNoLogin: no complete GDTF Share login is available.
	ErrNoLogin = errors.New("no GDTF Share login is saved")
	// ErrDisabled: the service has no library folder (tests, --demo).
	ErrDisabled = errors.New("the GDTF library has no folder in this mode")
	// ErrNotRunning: Trigger was called with no scheduler running.
	ErrNotRunning = errors.New("the GDTF sync scheduler is not running")
)

// OfflineError is a request that never reached GDTF Share (no network, DNS
// failure, timeout). It is distinct from GDTF Share answering with an error.
type OfflineError struct{ Err error }

func (e *OfflineError) Error() string { return "GDTF Share could not be reached: " + e.Err.Error() }
func (e *OfflineError) Unwrap() error { return e.Err }

// StatusError is GDTF Share answering with an HTTP status the code has no
// specific meaning for. Message is the JSON "error" text when one came back.
type StatusError struct {
	Op      string
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("GDTF Share %s answered HTTP %d: %s", e.Op, e.Code, e.Message)
	}
	return fmt.Sprintf("GDTF Share %s answered HTTP %d", e.Op, e.Code)
}

// Mode is one entry of getList's modes array. DMXFootprint is null when the
// list did not give a usable number: 0 is not "no answer".
type Mode struct {
	Name         string `json:"name"`
	DMXFootprint *int   `json:"dmxfootprint"`
}

// Entry is one getList item, normalised. The JSON keys are the API's own,
// so index.json can be compared with a raw getList answer by eye.
//
// Dates are Unix seconds (the page's table says Long; its example quotes
// them as strings, so both are accepted). 0 means the list did not give one.
type Entry struct {
	RID          int64    `json:"rid"`
	Fixture      string   `json:"fixture"`
	Manufacturer string   `json:"manufacturer"`
	Revision     string   `json:"revision"`
	CreationDate int64    `json:"creationDate"`
	LastModified int64    `json:"lastModified"`
	Uploader     string   `json:"uploader"`
	Rating       *float64 `json:"rating"`
	Version      string   `json:"version"`
	Creator      string   `json:"creator"`
	UUID         string   `json:"uuid"`
	FileSize     int64    `json:"filesize"`
	Modes        []Mode   `json:"modes"`
	// UploaderLabel is Uploader in words: Manufacturer, User or Unknown.
	UploaderLabel string `json:"uploaderLabel"`
}

// IndexEntry is one downloaded file.
type IndexEntry struct {
	Entry
	File         string    `json:"file"` // relative to the root: files/<rid>.gdtf
	SHA256       string    `json:"sha256"`
	LocalSize    int64     `json:"localSize"`
	DownloadedAt time.Time `json:"downloadedAt"`
}

// Pending is a download that did not complete; the next sync retries it.
type Pending struct {
	RID           int64     `json:"rid"`
	Manufacturer  string    `json:"manufacturer"`
	Fixture       string    `json:"fixture"`
	Revision      string    `json:"revision"`
	UploaderLabel string    `json:"uploaderLabel"`
	Error         string    `json:"error"`
	Attempts      int       `json:"attempts"`
	Since         time.Time `json:"since"`
}

// ManufacturerResult is the last sync's answer for one followed name.
// Matched false means GDTF Share has no manufacturer of that name; Similar
// lists names on GDTF Share that contain it (or that it contains), for the
// user to choose from — they are never followed automatically.
type ManufacturerResult struct {
	Name            string    `json:"name"`
	Matched         bool      `json:"matched"`
	ShareName       string    `json:"shareName"`
	ShareManufFiles int       `json:"shareManufacturerFiles"`
	ShareUserFiles  int       `json:"shareUserFiles"`
	LocalFiles      int       `json:"localFiles"`
	Downloaded      int       `json:"downloaded"`
	Replaced        int       `json:"replaced"`
	Failed          int       `json:"failed"`
	Result          string    `json:"result"`
	Similar         []string  `json:"similar"`
	CheckedAt       time.Time `json:"checkedAt"`
	NotYetChecked   bool      `json:"notYetChecked"`
}

// Result is the outcome of one sync attempt.
type Result struct {
	State      string    `json:"state"` // see the State* constants
	Message    string    `json:"message"`
	At         time.Time `json:"at"`
	Downloaded int       `json:"downloaded"`
	Replaced   int       `json:"replaced"`
	Unchanged  int       `json:"unchanged"`
	Failed     int       `json:"failed"`
	Relogins   int       `json:"relogins"`
}

// Sync states.
const (
	StateNever       = "never"        // no sync attempted yet
	StateSyncing     = "syncing"      // running now
	StateOK          = "ok"           // every wanted file is on disk
	StatePartial     = "partial"      // finished, some downloads pending
	StateOffline     = "offline"      // GDTF Share could not be reached
	StateRefused     = "refused"      // only route is the lighting adapter
	StateLoginFailed = "login-failed" // GDTF Share refused the login
	StateStopped     = "stopped"      // auth lost mid-sync, or cancelled
	StateError       = "error"        // anything else; Message says what
	StateNoLogin     = "no-login"     // nothing to log in with
)

// Progress is the running sync's position.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// Status is everything the UI needs to show the library's state. The login
// itself is not here: the web layer owns it and adds the user name only.
type Status struct {
	Enabled       bool                 `json:"enabled"`
	Running       bool                 `json:"running"`
	State         string               `json:"state"`
	Message       string               `json:"message"`
	Progress      Progress             `json:"progress"`
	LastAttempt   *time.Time           `json:"lastAttempt"`
	LastSync      *time.Time           `json:"lastSync"`
	LastResult    Result               `json:"lastResult"`
	Files         int                  `json:"files"`
	Followed      []string             `json:"followed"`
	Manufacturers []ManufacturerResult `json:"manufacturers"`
	Pending       []Pending            `json:"pending"`
	IntervalMin   int                  `json:"intervalMinutes"`
}

// SearchResult is one hit. Label is always set (API terms: every listed
// file says whether a manufacturer or a user uploaded it).
type SearchResult struct {
	Entry
	InLibrary bool `json:"inLibrary"`
}
